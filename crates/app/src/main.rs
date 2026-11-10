mod cli;
#[cfg(debug_assertions)]
mod fake_signals;
mod runtime;
mod tray;

use anyhow::{Context as _, Result};
use cli::{Mode, options};
use gpui::{App, Application, Bounds, QuitMode, WindowBounds, WindowOptions, prelude::*, px, size};
use runtime::{Runtime, Source};
use std::{
    cell::{Cell, RefCell},
    rc::Rc,
    time::Duration,
};
use tray_icon::menu::MenuEvent;
use wallpaper_core::Command;
use wallpaper_platform::MainThread;
use wallpaper_spike::storage::{Paths, Store};
use wallpaper_ui::Settings;

fn open_settings(runtime: Rc<RefCell<Runtime>>, cx: &mut App) -> Result<()> {
    if let Some(handle) = cx.windows().first() {
        handle.update(cx, |_, window, _| window.activate_window())?;
    } else {
        let bounds = Bounds::centered(None, size(px(620.), px(330.)), cx);
        cx.open_window(
            WindowOptions {
                window_bounds: Some(WindowBounds::Windowed(bounds)),
                ..Default::default()
            },
            |_, cx| {
                let source_runtime = runtime.clone();
                let source = Rc::new(move || {
                    let runtime = source_runtime.borrow();
                    runtime.snapshot()
                });
                let dispatch = Rc::new(move |command, cx: &mut App| match command {
                    Command::TogglePause => {
                        let main = MainThread::new().expect("GPUI foreground thread");
                        let result = runtime.borrow_mut().toggle(main);
                        if let Err(e) = result {
                            runtime.borrow_mut().fail(e);
                            cx.quit();
                        }
                    }
                });
                cx.new(|_| Settings::new(source, dispatch))
            },
        )?;
    }
    #[cfg(target_os = "macos")]
    cx.set_activation_policy(gpui::ActivationPolicy::Accessory);
    cx.activate(true);
    Ok(())
}
fn run(
    source: Source,
    status: Option<std::path::PathBuf>,
    config_dir: std::path::PathBuf,
    settings: wallpaper_core::config::PauseSettings,
    show_settings: bool,
) -> i32 {
    let exit_code = Rc::new(Cell::new(0));
    let runtime = Rc::new(RefCell::new(Runtime::new(
        source,
        status,
        exit_code.clone(),
        settings,
    )));
    let app: Application = gpui_platform::application();
    // Configure before NSApplication launches, so it never registers a Dock icon.
    #[cfg(target_os = "macos")]
    let app = app.with_windowing(gpui::WindowingRequest::Headless);
    let owner = runtime.clone();
    app.run(move |cx| {
        cx.set_quit_mode(QuitMode::Explicit);
        #[cfg(target_os = "macos")]
        cx.set_activation_policy(gpui::ActivationPolicy::Accessory);
        let cleanup = owner.clone();
        cx.on_app_quit(move |_| {
            cleanup
                .borrow_mut()
                .stop(MainThread::new().expect("GPUI quit on main thread"));
            // NSApplication terminate does not return from app.run. stop has
            // synchronously written (and synced) the final status before this exit.
            #[cfg(target_os = "macos")]
            {
                let code = cleanup.borrow().exit_code.get();
                if code != 0 {
                    std::process::exit(code);
                }
            }
            async {}
        })
        .detach();
        cx.spawn(async move |cx| {
            // Resume only after the platform run loop has started. The timer is
            // scheduled by GPUI, and this !Send future always resumes on foreground.
            cx.background_executor()
                .timer(Duration::from_millis(1))
                .await;
            let main = MainThread::new().expect("GPUI foreground thread");
            let initialized = owner.borrow_mut().initialize(main);
            if let Err(e) = initialized {
                owner.borrow_mut().fail(e);
                cx.update(|cx| cx.quit());
                return;
            }
            if show_settings && let Err(e) = cx.update(|cx| open_settings(owner.clone(), cx)) {
                owner.borrow_mut().fail(e);
                cx.update(|cx| cx.quit());
                return;
            }
            loop {
                cx.background_executor()
                    .timer(Duration::from_millis(100))
                    .await;
                let mut quit = wallpaper_platform::exit_requested();
                let mut open = false;
                let mut toggle = false;
                while let Ok(event) = MenuEvent::receiver().try_recv() {
                    let runtime = owner.borrow();
                    if let Some(tray) = &runtime.tray {
                        open |= event.id == *tray.open.id();
                        toggle |= event.id == *tray.pause.id();
                        quit |= event.id == *tray.quit.id();
                    }
                }
                open |= wallpaper_platform::activation_requested(&config_dir);
                // tray-icon's hidden native window uses GPUI's Win32 message pump.
                while tray_icon::TrayIconEvent::receiver().try_recv().is_ok() {}
                if quit {
                    cx.update(|cx| cx.quit());
                    return;
                }
                let result = (|| -> Result<()> {
                    if toggle {
                        owner.borrow_mut().toggle(main)?;
                    }
                    if open {
                        cx.update(|cx| open_settings(owner.clone(), cx))?;
                    }
                    owner.borrow_mut().tick(main)
                })();
                if let Err(e) = result {
                    owner.borrow_mut().fail(e);
                    cx.update(|cx| cx.quit());
                    return;
                }
                if std::mem::take(&mut owner.borrow_mut().published) {
                    cx.update(|cx| cx.refresh_windows());
                }
            }
        })
        .detach();
    });
    runtime
        .borrow_mut()
        .stop(MainThread::new().expect("main thread"));
    exit_code.get()
}
fn main() {
    if std::env::args_os().nth(1).as_deref() == Some(std::ffi::OsStr::new("--signals-probe")) {
        std::process::exit(cli::signals_probe());
    }
    if std::env::args_os().nth(1).as_deref() == Some(std::ffi::OsStr::new("--media-probe")) {
        std::process::exit(cli::media_probe(std::env::args_os().skip(2)));
    }
    let logger = wallpaper_spike::logging::Logger::install();
    #[cfg(windows)]
    if std::env::args().nth(1).as_deref() == Some("--mf-worker") {
        let result = std::env::args()
            .nth(2)
            .context("missing pipe name")
            .and_then(|name| wallpaper_platform::worker_main(&name));
        if let Err(e) = result {
            log::error!("mf worker: {e:#}");
            std::process::exit(1);
        }
        return;
    }
    let result = (|| -> Result<i32> {
        MainThread::new().context("must start on main thread")?;
        wallpaper_platform::install_exit_handler()?;
        let mode = options()?;
        let paths = Paths::from_env()?;
        if let Mode::ExportDiagnostics(dst) = &mode {
            let main = MainThread::new().context("main thread required")?;
            let displays = wallpaper_platform::displays(main).ok();
            let known: Vec<_> = ["HOME", "USERPROFILE", "USER", "USERNAME"]
                .iter()
                .filter_map(|key| std::env::var(key).ok())
                .collect();
            wallpaper_spike::diagnostics::export(&paths, dst, displays.as_deref(), &known)?;
            println!("{}", dst.display());
            return Ok(0);
        }
        let startup_backend = if matches!(mode, Mode::LaunchAtLogin(_)) {
            Some(wallpaper_platform::startup::Backend::from_env()?)
        } else {
            None
        };
        if let Mode::LaunchAtLogin(None) = mode {
            println!(
                "{}",
                wallpaper_spike::startup::status(
                    &paths,
                    startup_backend.as_ref().unwrap(),
                    &std::env::current_exe()?
                )?
            );
            return Ok(0);
        }
        // Legacy import retains its pre-lock preflight. Run inspects after Primary
        // so preflight failures can be recorded; backup recovery stays in memory.
        if matches!(
            mode,
            Mode::Library(cli::LibraryCommand::LegacyImport { .. })
        ) {
            let report = Store::inspect(&paths);
            if report.has_errors() {
                eprintln!(
                    "{}\n请使用 --store-check 检查，或 --store-reset 重置",
                    serde_json::to_string_pretty(&report.summary())?
                );
                return Ok(2);
            }
        }
        let activate = matches!(mode, Mode::Run { .. } | Mode::Play(_));
        match wallpaper_platform::instance::acquire_with_activation(&paths.config, activate)? {
            wallpaper_platform::Acquire::Primary => {}
            wallpaper_platform::Acquire::Secondary => {
                if matches!(
                    mode,
                    Mode::StoreCheck | Mode::StoreReset | Mode::Library(_) | Mode::LaunchAtLogin(_)
                ) {
                    eprintln!("已有实例在运行，未执行存储操作");
                    return Ok(4);
                }
                eprintln!("已有实例，已请求其显示窗口");
                return Ok(0);
            }
            wallpaper_platform::Acquire::LegacyRunning => {
                eprintln!("旧版正在运行，请先退出旧版");
                return Ok(3);
            }
        }
        if matches!(mode, Mode::Run { .. }) {
            logger.enable(&paths.logs);
        }
        match mode {
            Mode::LaunchAtLogin(Some(enabled)) => wallpaper_spike::startup::set(
                &paths,
                startup_backend.as_ref().unwrap(),
                &std::env::current_exe()?,
                enabled,
            ),
            Mode::LaunchAtLogin(None) | Mode::ExportDiagnostics(_) => unreachable!(),
            Mode::Library(command) => cli::run_library(command, paths),
            Mode::Play(options) => Ok(run(
                Source::Play(options.video),
                options.status,
                paths.config,
                wallpaper_core::config::PauseSettings::default(),
                true,
            )),
            Mode::Run { status } => {
                let report = Store::inspect(&paths);
                if report.has_errors() {
                    log::error!("store preflight failed");
                    eprintln!(
                        "{}\n请使用 --store-check 检查，或 --store-reset 重置",
                        serde_json::to_string_pretty(&report.summary())?
                    );
                    return Ok(2);
                }
                let settings = report.config.value.clone().unwrap_or_default();
                let show_settings =
                    wallpaper_spike::startup::should_open_settings(false, &settings);
                Ok(run(
                    Source::Assigned {
                        assignments: report.assignments.value.unwrap_or_default(),
                        library: report.library.value.unwrap_or_default(),
                        library_root: paths.library,
                    },
                    status,
                    paths.config,
                    settings.pause,
                    show_settings,
                ))
            }
            Mode::StoreCheck => {
                let report = Store::open(&paths);
                println!("{}", serde_json::to_string_pretty(&report.summary())?);
                Ok(if report.has_errors() { 2 } else { 0 })
            }
            Mode::StoreReset => {
                for path in Store::reset(&paths)? {
                    println!("{}", path.display());
                }
                Ok(0)
            }
        }
    })();
    let code = match result {
        Ok(code) => code,
        Err(e) => {
            eprintln!("wallpaper-spike: {e:#}");
            1
        }
    };
    std::process::exit(code);
}
