mod power;
mod visibility;
pub use power::power;
pub use visibility::visibility;
mod displays;
mod image;
pub(crate) mod probe;
use anyhow::{Context, Result, bail};
pub use displays::displays;
use objc2::{MainThreadMarker, MainThreadOnly, msg_send, rc::Retained, runtime::AnyObject};
use objc2_app_kit::{
    NSBackingStoreType, NSColor, NSView, NSWindow, NSWindowCollectionBehavior,
    NSWindowOcclusionState, NSWindowStyleMask,
};
use objc2_av_foundation::{
    AVLayerVideoGravityResize, AVLayerVideoGravityResizeAspect,
    AVLayerVideoGravityResizeAspectFill, AVPlayerItem, AVPlayerLayer, AVPlayerLooper,
    AVQueuePlayer,
};
use objc2_core_graphics::{
    CGColor, CGWindowLevelForKey, CGWindowLevelKey, CGWindowListCopyWindowInfo, CGWindowListOption,
    kCGWindowIsOnscreen,
};
use objc2_foundation::{NSString, NSURL};
use objc2_quartz_core::{
    CAAutoresizingMask, CALayer, CATransaction, kCAGravityResize, kCAGravityResizeAspect,
    kCAGravityResizeAspectFill,
};
use std::sync::mpsc::{Receiver, TryRecvError};
use wallpaper_core::{
    Evidence, Truth,
    displays::DisplayId,
    library::{Fit, Kind, Media},
};

pub type MainThread = MainThreadMarker;

pub fn displays_asleep(_main: MainThread) -> bool {
    objc2_core_graphics::CGDisplayIsAsleep(objc2_core_graphics::CGMainDisplayID())
}

pub fn session_locked(_mtm: MainThread) -> bool {
    let Some(info) = objc2_core_graphics::CGSessionCopyCurrentDictionary() else {
        crate::signal_health("session lock", false);
        return false;
    };
    crate::signal_health("session lock", true);
    // The copied CFDictionary owns the borrowed NSNumber for this query.
    // An absent lock key is normal for an unlocked session.
    unsafe {
        let dictionary = &*((&*info as *const _) as *const AnyObject);
        let key = NSString::from_str("CGSSessionScreenIsLocked");
        let value: *const AnyObject = msg_send![dictionary, objectForKey: &*key];
        if value.is_null() {
            false
        } else {
            msg_send![value, boolValue]
        }
    }
}

pub struct Desktop {
    // The marker makes the complete owner !Send/!Sync, including AV objects.
    mtm: MainThreadMarker,
    window: Retained<NSWindow>,
    view: Retained<NSView>,
    content: Content,
    closed: bool,
    display: DisplayId,
}
enum Content {
    Video {
        player: Retained<AVQueuePlayer>,
        looper: Retained<AVPlayerLooper>,
        layer: Retained<AVPlayerLayer>,
    },
    Image {
        layer: Retained<CALayer>,
        pending: Option<Receiver<Result<image::Decoded>>>,
    },
}
impl Desktop {
    pub const BACKEND: &'static str = "avf";
    pub fn obscured(evidence: &Evidence) -> bool {
        evidence.visible == Truth::Yes
            && evidence.attached == Truth::Yes
            && evidence.on_screen == Truth::No
    }
    pub fn new(display: &DisplayId, media: &Media, paused: bool, mtm: MainThread) -> Result<Self> {
        // All AppKit/AVFoundation access is inside methods requiring a main-thread
        // token; Drop carries the original token and cannot migrate to a worker.
        unsafe {
            let (_, screen) = displays::screens(mtm)?
                .into_iter()
                .find(|(info, _)| &info.id == display)
                .context("mac: display not found")?;
            let mut content = screen.frame();
            content.origin = objc2_foundation::NSPoint::new(0.0, 0.0);
            let window = NSWindow::initWithContentRect_styleMask_backing_defer_screen(
                NSWindow::alloc(mtm),
                content,
                NSWindowStyleMask::Borderless,
                NSBackingStoreType::Buffered,
                false,
                Some(&screen),
            );
            window.setFrame_display(screen.frame(), false);
            window.setReleasedWhenClosed(false);
            window.setLevel(CGWindowLevelForKey(CGWindowLevelKey::DesktopWindowLevelKey) as isize);
            window.setCollectionBehavior(
                NSWindowCollectionBehavior::CanJoinAllSpaces
                    | NSWindowCollectionBehavior::Stationary
                    | NSWindowCollectionBehavior::IgnoresCycle,
            );
            window.setIgnoresMouseEvents(true);
            window.setHasShadow(false);
            window.setOpaque(false);
            window.setBackgroundColor(Some(&NSColor::clearColor()));
            let Some(view) = window.contentView() else {
                window.close();
                bail!("mac: missing content view");
            };
            view.setWantsLayer(true);
            let Some(root) = view.layer() else {
                window.close();
                bail!("mac: missing root layer");
            };
            let black = CGColor::new_generic_rgb(0., 0., 0., 1.);
            let content = match media.kind {
                Kind::Video => {
                    let gravity = match media.fit {
                        Fit::Fill => AVLayerVideoGravityResizeAspectFill,
                        Fit::Fit => AVLayerVideoGravityResizeAspect,
                        Fit::Stretch => AVLayerVideoGravityResize,
                    }
                    .context("avf: missing gravity constant")?;
                    let url = NSURL::fileURLWithPath(&NSString::from_str(&media.path));
                    let item = AVPlayerItem::playerItemWithURL(&url, mtm);
                    let player = AVQueuePlayer::new(mtm);
                    player.setMuted(true);
                    let looper =
                        AVPlayerLooper::playerLooperWithPlayer_templateItem(&player, &item);
                    let layer = AVPlayerLayer::playerLayerWithPlayer(Some(&player));
                    layer.setVideoGravity(gravity);
                    prepare_layer(&layer, &root, &black);
                    if !paused {
                        player.play();
                    }
                    Content::Video {
                        player,
                        looper,
                        layer,
                    }
                }
                Kind::Image => {
                    let scale = screen.backingScaleFactor();
                    let pending = match image::start(
                        media.path.clone(),
                        (content.size.width * scale).ceil() as u32,
                        (content.size.height * scale).ceil() as u32,
                    ) {
                        Ok(pending) => pending,
                        Err(e) => {
                            window.close();
                            return Err(e);
                        }
                    };
                    let layer = CALayer::layer();
                    layer.setContentsGravity(match media.fit {
                        Fit::Fill => kCAGravityResizeAspectFill,
                        Fit::Fit => kCAGravityResizeAspect,
                        Fit::Stretch => kCAGravityResize,
                    });
                    layer.setContentsScale(scale);
                    layer.setMasksToBounds(true);
                    prepare_layer(&layer, &root, &black);
                    Content::Image {
                        layer,
                        pending: Some(pending),
                    }
                }
            };
            Ok(Self {
                mtm,
                window,
                view,
                content,
                closed: false,
                display: display.clone(),
            })
        }
    }
    pub fn reposition(&mut self, main: MainThread) -> Result<()> {
        if let Some((_, screen)) = displays::screens(main)?
            .into_iter()
            .find(|(info, _)| info.id == self.display)
        {
            let frame = screen.frame();
            if self.window.frame() != frame {
                self.window.setFrame_display(frame, true);
            }
        }
        Ok(())
    }
    pub fn set_restart_enabled(&mut self, _enabled: bool) {}
    pub fn poll(&mut self, mtm: MainThread, paused: bool) -> Result<Option<Evidence>> {
        unsafe {
            if self.closed {
                bail!("avf: player closed");
            }
            let mut e = Evidence::default();
            match &mut self.content {
                Content::Video {
                    player,
                    looper,
                    layer,
                } => {
                    if player.status().0 == 2 || looper.status().0 == 2 {
                        bail!(
                            "avf: {}",
                            player
                                .error()
                                .or_else(|| looper.error())
                                .map(|e| e.localizedDescription().to_string())
                                .unwrap_or_else(|| "player or looper failed".into())
                        );
                    }
                    if let Some(item) = player.currentItem() {
                        if item.status().0 == 2 {
                            bail!(
                                "avf: {}",
                                item.error()
                                    .map(|e| e.localizedDescription().to_string())
                                    .unwrap_or_else(|| "item failed".into())
                            );
                        }
                        if !paused && player.rate() == 0.0 {
                            log::info!("avf: non-user pause detected; resuming playback");
                            player.play();
                        }
                        if layer.isReadyForDisplay() {
                            CATransaction::begin();
                            CATransaction::setDisableActions(true);
                            layer.setHidden(false);
                            CATransaction::commit();
                            if !self.window.isVisible() {
                                self.window.orderFront(None);
                            }
                        }
                        e.ready_for_display = (layer.isReadyForDisplay()
                            && !layer.isHidden()
                            && layer.superlayer().is_some())
                        .into();
                        let time = player.currentTime();
                        if time.flags.0 & 1 != 0
                            && time.flags.0 & (4 | 8 | 16) == 0
                            && time.timescale > 0
                            && time.value >= 0
                        {
                            e.position = Some(time.value as f64 / time.timescale as f64);
                        }
                        e.loops = u64::try_from(looper.loopCount()).ok();
                    }
                }
                Content::Image { layer, pending } => {
                    if let Some(receive) = pending {
                        match receive.try_recv() {
                            Ok(result) => {
                                let decoded = result?;
                                CATransaction::begin();
                                CATransaction::setDisableActions(true);
                                // CALayer retains the CGImage assigned as contents.
                                let object = &*((&*decoded.0
                                    as *const objc2_core_graphics::CGImage)
                                    .cast::<AnyObject>());
                                layer.setContents(Some(object));
                                layer.setHidden(false);
                                CATransaction::commit();
                                *pending = None;
                                self.window.orderFront(None);
                            }
                            Err(TryRecvError::Empty) => {}
                            Err(TryRecvError::Disconnected) => bail!("image: decoder disconnected"),
                        }
                    }
                    e.ready_for_display = (layer.contents().is_some()
                        && !layer.isHidden()
                        && layer.superlayer().is_some())
                    .into();
                }
            }
            e.window = self.window.windowNumber() as usize;
            e.visible = self.window.isVisible().into();
            e.attached = (self.window.screen().is_some()
                && self.window.contentView().as_deref() == Some(&*self.view))
            .into();
            e.occluded = (!self
                .window
                .occlusionState()
                .contains(NSWindowOcclusionState::Visible))
            .into();
            e.on_screen = on_screen(e.window, mtm);
            Ok(Some(e))
        }
    }
    pub fn take_restarted(&mut self) -> bool {
        false
    }
    pub fn after_gap(&mut self) {}
    pub fn confirmed(&mut self) {}
    pub fn set_paused(&mut self, paused: bool, _mtm: MainThread) -> Result<()> {
        unsafe {
            if let Content::Video { player, .. } = &self.content {
                if paused {
                    player.pause();
                } else {
                    player.play();
                }
            }
        }
        Ok(())
    }
    pub fn close(&mut self, _mtm: MainThread) -> Result<()> {
        if self.closed {
            return Ok(());
        }
        unsafe {
            match &mut self.content {
                Content::Video {
                    player,
                    looper,
                    layer,
                } => {
                    player.pause();
                    looper.disableLooping();
                    layer.removeFromSuperlayer();
                    layer.setPlayer(None);
                }
                Content::Image { layer, pending } => {
                    pending.take();
                    layer.removeFromSuperlayer();
                    layer.setContents(None);
                }
            }
            self.window.orderOut(None);
            self.window.close();
        }
        self.closed = true;
        Ok(())
    }
}
fn prepare_layer(layer: &CALayer, root: &CALayer, black: &CGColor) {
    CATransaction::begin();
    CATransaction::setDisableActions(true);
    layer.setHidden(true);
    layer.setBackgroundColor(Some(black));
    layer.setFrame(root.bounds());
    layer.setAutoresizingMask(
        CAAutoresizingMask::LayerWidthSizable | CAAutoresizingMask::LayerHeightSizable,
    );
    root.addSublayer(layer);
    CATransaction::commit();
}
impl Drop for Desktop {
    fn drop(&mut self) {
        if let Err(e) = self.close(self.mtm) {
            log::error!("mac cleanup: {e:#}");
        }
    }
}
fn on_screen(number: usize, _mtm: MainThreadMarker) -> Truth {
    let Some(list) = CGWindowListCopyWindowInfo(CGWindowListOption(8), number as u32) else {
        return Truth::Unknown;
    };
    // CFArray/CFDictionary are toll-free bridged. Copy keeps the complete metadata
    // graph alive while these borrowed Objective-C references are inspected.
    unsafe {
        let array = &*((&*list as *const _) as *const AnyObject);
        let count: usize = msg_send![array, count];
        if count == 0 {
            return Truth::No;
        }
        let info: *const AnyObject = msg_send![array, objectAtIndex: 0usize];
        let key = (kCGWindowIsOnscreen as *const _) as *const AnyObject;
        let value: *const AnyObject = msg_send![info, objectForKey: key];
        if value.is_null() {
            Truth::Unknown
        } else {
            let yes: bool = msg_send![value, boolValue];
            yes.into()
        }
    }
}

#[cfg(test)]
mod recovery_tests {
    use super::*;

    #[test]
    fn obscured_requires_all_three_surface_facts() {
        for visible in [Truth::Yes, Truth::No, Truth::Unknown] {
            for attached in [Truth::Yes, Truth::No, Truth::Unknown] {
                for on_screen in [Truth::Yes, Truth::No, Truth::Unknown] {
                    let evidence = Evidence {
                        visible,
                        attached,
                        on_screen,
                        ..Evidence::default()
                    };
                    assert_eq!(
                        Desktop::obscured(&evidence),
                        visible == Truth::Yes && attached == Truth::Yes && on_screen == Truth::No
                    );
                }
            }
        }
    }
}
