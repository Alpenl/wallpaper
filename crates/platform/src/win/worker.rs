use super::{Command, Observation, pipe::Pipe};
use anyhow::{Context, Result, bail};
use std::{
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicU64, Ordering},
    },
    time::{Duration, Instant},
};
use wallpaper_core::{
    Evidence, Truth,
    fit::place,
    library::{Fit, Kind},
};
use windows::{
    Win32::{
        Foundation::*,
        Graphics::{
            Direct3D::*,
            Direct3D10::ID3D10Multithread,
            Direct3D11::*,
            Dxgi::{Common::*, *},
        },
        Media::MediaFoundation::*,
        System::Com::*,
    },
    core::{BSTR, Interface, implement},
};

#[derive(Default)]
struct Events {
    playable: AtomicBool,
    error: AtomicU64,
}
#[implement(IMFMediaEngineNotify)]
struct Notify(Arc<Events>);
impl IMFMediaEngineNotify_Impl for Notify_Impl {
    fn EventNotify(&self, event: u32, param1: usize, param2: u32) -> windows::core::Result<()> {
        if event == MF_MEDIA_ENGINE_EVENT_CANPLAY.0 as u32 {
            self.0.playable.store(true, Ordering::Release);
        }
        if event == MF_MEDIA_ENGINE_EVENT_ERROR.0 as u32 {
            self.0
                .error
                .store(((param1 as u64) << 32) | param2 as u64, Ordering::Release);
        }
        Ok(())
    }
}
struct Runtime {
    closed: bool,
}
impl Runtime {
    fn new() -> Result<Self> {
        unsafe {
            CoInitializeEx(None, COINIT_MULTITHREADED).ok()?;
            if let Err(e) = MFStartup(MF_VERSION, MFSTARTUP_FULL) {
                CoUninitialize();
                return Err(e.into());
            }
        }
        Ok(Self { closed: false })
    }
    fn close(&mut self) -> Result<()> {
        if !self.closed {
            let result = unsafe { MFShutdown() };
            unsafe {
                CoUninitialize();
            }
            self.closed = true;
            result?;
        }
        Ok(())
    }
}
impl Drop for Runtime {
    fn drop(&mut self) {
        if let Err(e) = self.close() {
            log::error!("MF runtime cleanup: {e:#}");
        }
    }
}
pub(super) struct RenderTarget {
    pub device: ID3D11Device,
    pub swap: IDXGISwapChain1,
    pub texture: ID3D11Texture2D,
}
impl RenderTarget {
    pub fn new(window: usize, width: u32, height: u32) -> Result<Self> {
        if window == 0 || width == 0 || height == 0 || width > 16384 || height > 16384 {
            bail!("invalid render target");
        }
        unsafe {
            let mut device = None;
            D3D11CreateDevice(
                None,
                D3D_DRIVER_TYPE_HARDWARE,
                HMODULE::default(),
                D3D11_CREATE_DEVICE_BGRA_SUPPORT | D3D11_CREATE_DEVICE_VIDEO_SUPPORT,
                None,
                D3D11_SDK_VERSION,
                Some(&mut device),
                None,
                None,
            )?;
            let device = device.context("mf: no D3D11 device")?;
            let protected: ID3D10Multithread = device.cast()?;
            let _ = protected.SetMultithreadProtected(true);
            let dxgi: IDXGIDevice = device.cast()?;
            let factory: IDXGIFactory2 = dxgi.GetAdapter()?.GetParent()?;
            let description = DXGI_SWAP_CHAIN_DESC1 {
                Width: width,
                Height: height,
                Format: DXGI_FORMAT_B8G8R8A8_UNORM,
                SampleDesc: DXGI_SAMPLE_DESC {
                    Count: 1,
                    Quality: 0,
                },
                BufferUsage: DXGI_USAGE_RENDER_TARGET_OUTPUT,
                BufferCount: 2,
                SwapEffect: DXGI_SWAP_EFFECT_FLIP_DISCARD,
                ..Default::default()
            };
            let hwnd = HWND(window as *mut _);
            let swap = factory.CreateSwapChainForHwnd(&device, hwnd, &description, None, None)?;
            let texture = swap.GetBuffer(0)?;
            Ok(Self {
                device,
                swap,
                texture,
            })
        }
    }
}
struct Engine {
    engine: IMFMediaEngine,
    render: RenderTarget,
    _manager: IMFDXGIDeviceManager,
    fit: Fit,
    events: Arc<Events>,
    width: u32,
    height: u32,
    evidence: Evidence,
    last_pts: Option<i64>,
    paused: bool,
    last_recover: Instant,
    closed: bool,
    diagnostic_events: Vec<String>,
}
impl Engine {
    fn new(
        path: &str,
        window: usize,
        width: u32,
        height: u32,
        paused: bool,
        fit: Fit,
    ) -> Result<Self> {
        if window == 0 || width == 0 || height == 0 || width > 16384 || height > 16384 {
            bail!("mf: invalid render target");
        }
        unsafe {
            let render = RenderTarget::new(window, width, height)?;
            let mut manager = None;
            let mut token = 0;
            MFCreateDXGIDeviceManager(&mut token, &mut manager)?;
            let manager = manager.context("mf: no DXGI manager")?;
            manager.ResetDevice(&render.device, token)?;
            let mut attributes = None;
            MFCreateAttributes(&mut attributes, 3)?;
            let attributes = attributes.context("mf: no attributes")?;
            let events = Arc::new(Events::default());
            let notify: IMFMediaEngineNotify = Notify(events.clone()).into();
            attributes.SetUnknown(&MF_MEDIA_ENGINE_CALLBACK, &notify)?;
            attributes.SetUnknown(&MF_MEDIA_ENGINE_DXGI_MANAGER, &manager)?;
            attributes.SetUINT32(
                &MF_MEDIA_ENGINE_VIDEO_OUTPUT_FORMAT,
                DXGI_FORMAT_B8G8R8A8_UNORM.0 as u32,
            )?;
            let factory: IMFMediaEngineClassFactory =
                CoCreateInstance(&CLSID_MFMediaEngineClassFactory, None, CLSCTX_INPROC_SERVER)?;
            let engine = factory.CreateInstance(0, &attributes)?;
            let instance = Self {
                engine,
                render,
                _manager: manager,
                fit,
                events,
                width,
                height,
                evidence: Evidence {
                    presented_frames: Some(0),
                    loops: Some(0),
                    child_pid: std::process::id(),
                    ..Evidence::default()
                },
                diagnostic_events: Vec::new(),
                last_pts: None,
                paused,
                last_recover: Instant::now() - Duration::from_secs(2),
                closed: false,
            };
            instance.engine.SetMuted(true)?;
            instance.engine.SetLoop(true)?;
            instance.engine.SetAutoPlay(false)?;
            instance.engine.SetSource(&BSTR::from(path))?;
            instance.engine.Load()?;
            Ok(instance)
        }
    }
    fn set_paused(&mut self, paused: bool) -> Result<()> {
        if self.paused != paused {
            unsafe {
                if paused {
                    self.engine.Pause()?;
                } else {
                    self.engine.Play()?;
                }
            }
            self.paused = paused;
        }
        Ok(())
    }
    fn tick(&mut self) -> Result<()> {
        let error = self.events.error.load(Ordering::Acquire);
        if error != 0 {
            bail!(
                "media engine error code={} HRESULT=0x{:08x}",
                error >> 32,
                error as u32
            );
        }
        if !self.events.playable.load(Ordering::Acquire) {
            return Ok(());
        }
        unsafe {
            if !self.paused && self.last_recover.elapsed() >= Duration::from_secs(2) {
                if self.engine.IsPaused().as_bool() || self.engine.GetPlaybackRate() == 0.0 {
                    self.engine.SetPlaybackRate(1.0)?;
                    self.engine.Play()?;
                    if self.diagnostic_events.len() < 8 {
                        self.diagnostic_events.push("playback recovered".into());
                    }
                }
                self.last_recover = Instant::now();
            }
            // The generated Result<i64> wrapper erases S_FALSE. Call the typed
            // vtable so 'no new frame' never increments presented_frames.
            let mut pts = 0;
            let hr = (Interface::vtable(&self.engine).OnVideoStreamTick)(
                Interface::as_raw(&self.engine),
                &mut pts,
            );
            if hr == S_FALSE {
                return Ok(());
            }
            hr.ok()?;
            let mut aw = 0;
            let mut ah = 0;
            self.engine
                .GetVideoAspectRatio(Some(&mut aw), Some(&mut ah))?;
            if aw == 0 || ah == 0 {
                bail!("mf: invalid video aspect {aw}x{ah}");
            }
            let placement = place(aw, ah, self.width, self.height, self.fit)
                .context("mf: invalid video placement")?;
            let [left, top, right, bottom] = placement.source;
            let crop = MFVideoNormalizedRect {
                left,
                top,
                right,
                bottom,
            };
            let [left, top, right, bottom] = placement.dest;
            let dest = RECT {
                left: left as i32,
                top: top as i32,
                right: right as i32,
                bottom: bottom as i32,
            };
            if self.fit == Fit::Fit {
                let mut view = None;
                self.render.device.CreateRenderTargetView(
                    &self.render.texture,
                    None,
                    Some(&mut view),
                )?;
                self.render
                    .device
                    .GetImmediateContext()?
                    .ClearRenderTargetView(
                        &view.context("mf: no render target view")?,
                        &[0., 0., 0., 1.],
                    );
            }
            self.engine
                .TransferVideoFrame(&self.render.texture, Some(&crop), &dest, None)?;
            let result = self.render.swap.Present(0, DXGI_PRESENT(0));
            result.ok()?;
            if result == DXGI_STATUS_OCCLUDED {
                let position = self.engine.GetCurrentTime();
                self.evidence.position =
                    (position.is_finite() && position >= 0.).then_some(position);
                self.evidence.ready_for_display = Truth::Yes;
                return Ok(());
            }
            if result != S_OK {
                return Ok(());
            }
            self.evidence.ready_for_display = Truth::Yes;
            *self.evidence.presented_frames.as_mut().unwrap() += 1;
            if self.last_pts.is_some_and(|last| pts < last) {
                *self.evidence.loops.as_mut().unwrap() += 1;
            }
            self.last_pts = Some(pts);
            let position = self.engine.GetCurrentTime();
            self.evidence.position = (position.is_finite() && position >= 0.).then_some(position);
        }
        Ok(())
    }
    fn close(&mut self) -> Result<()> {
        if !self.closed {
            unsafe {
                self.engine.Shutdown()?;
            }
            self.closed = true;
        }
        Ok(())
    }
}
impl Drop for Engine {
    fn drop(&mut self) {
        if let Err(e) = self.close() {
            log::error!("media engine shutdown: {e:#}");
        }
    }
}
pub fn worker_main(name: &str) -> Result<()> {
    let pipe = Pipe::client(name)?;
    let mut runtime = Runtime::new()?; // Only this child process initializes MF/MTA.
    let mut engine: Option<Engine> = None;
    let mut image: Option<super::image::Image> = None;
    let mut failure = String::new();
    let mut diagnostic_events = Vec::new();
    loop {
        let command = match pipe.receive::<Command>() {
            Ok(command) => command,
            Err(e) => {
                if let Some(engine) = &mut engine {
                    engine.close()?;
                }
                return Err(e);
            }
        };
        if let Some(command) = command {
            match command {
                Command::Stop => {
                    if let Some(engine) = &mut engine {
                        engine.close()?;
                    }
                    drop(engine);
                    drop(image);
                    runtime.close()?;
                    return Ok(());
                }
                Command::Load {
                    path,
                    kind,
                    fit,
                    window,
                    width,
                    height,
                    paused,
                } => {
                    if engine.is_some() || image.is_some() {
                        bail!("mf: duplicate load");
                    }
                    let result = match kind {
                        Kind::Video => Engine::new(&path, window, width, height, paused, fit)
                            .map(|value| engine = Some(value)),
                        Kind::Image => super::image::Image::new(&path, window, width, height, fit)
                            .map(|value| image = Some(value)),
                    };
                    if let Err(e) = result {
                        diagnostic_events.push(error_event(&e));
                        failure = format!("load: {e:#}");
                    }
                }
                Command::Sample { paused } => {
                    if let Some(engine) = &mut engine
                        && let Err(e) = engine.set_paused(paused)
                    {
                        if diagnostic_events.len() < 8 {
                            diagnostic_events.push(error_event(&e));
                        }
                        failure = format!("pause: {e:#}");
                    }
                }
            }
            let evidence = engine
                .as_ref()
                .map(|e| e.evidence.clone())
                .or_else(|| image.as_ref().map(|i| i.evidence.clone()))
                .unwrap_or_default();
            if let Some(engine) = &mut engine {
                for event in engine.diagnostic_events.drain(..) {
                    if diagnostic_events.len() < 8 {
                        diagnostic_events.push(event);
                    }
                }
            }
            pipe.send(&Observation {
                evidence,
                reason: failure.clone(),
                events: std::mem::take(&mut diagnostic_events),
            })?;
        }
        if failure.is_empty()
            && let Some(engine) = &mut engine
            && let Err(e) = engine.tick()
        {
            if diagnostic_events.len() < 8 {
                diagnostic_events.push(error_event(&e));
            }
            failure = format!("render: {e:#}");
        }
        // Bounded polling, including while locked/occluded; avoids an unbounded
        // WaitForVBlank blocking pipe commands or graceful shutdown.
        std::thread::sleep(Duration::from_millis(8));
    }
}

fn error_event(error: &anyhow::Error) -> String {
    let code = error
        .downcast_ref::<windows::core::Error>()
        .map_or(0x80004005u32, |e| e.code().0 as u32);
    format!("error code=0x{code:08x}")
}
