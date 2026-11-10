use anyhow::{Context as _, Result, bail};
use std::path::PathBuf;

pub(crate) struct Options {
    pub(crate) video: String,
    pub(crate) status: Option<PathBuf>,
}
pub(crate) enum Mode {
    Run { status: Option<PathBuf> },
    Play(Options),
    Library(LibraryCommand),
    StoreCheck,
    StoreReset,
    LaunchAtLogin(Option<bool>),
    ExportDiagnostics(PathBuf),
}

pub(crate) enum LibraryCommand {
    Import(Vec<PathBuf>),
    LegacyImport {
        from: Option<PathBuf>,
        library: Option<PathBuf>,
    },
    List {
        query: String,
    },
    Delete {
        id: String,
        yes: bool,
    },
    Assign {
        id: String,
        display: Option<String>,
    },
    SetFit {
        id: String,
        fit: wallpaper_core::library::Fit,
    },
    Displays,
}

pub(crate) fn options() -> Result<Mode> {
    parse(std::env::args().skip(1))
}

fn parse(args: impl Iterator<Item = String>) -> Result<Mode> {
    let values: Vec<_> = args.collect();
    if values
        .first()
        .is_some_and(|v| v == "--launch-at-login" || v == "--export-diagnostics")
    {
        anyhow::ensure!(values.len() == 2, "option requires exactly one argument");
        return match values[0].as_str() {
            "--export-diagnostics" => Ok(Mode::ExportDiagnostics(values[1].clone().into())),
            _ => Ok(Mode::LaunchAtLogin(match values[1].as_str() {
                "on" => Some(true),
                "off" => Some(false),
                "status" => None,
                _ => bail!("--launch-at-login requires on|off|status"),
            })),
        };
    }
    if values.first().is_some_and(|arg| {
        matches!(
            arg.as_str(),
            "--legacy-import"
                | "--import"
                | "--library-list"
                | "--delete"
                | "--assign"
                | "--set-fit"
                | "--displays"
        )
    }) {
        return parse_library(&values).map(Mode::Library);
    }
    let mut args = values.into_iter();
    let mut store_mode = None;
    let mut video = None;
    let mut status = None;
    while let Some(arg) = args.next() {
        match arg.as_str() {
            "--store-check" | "--store-reset" => {
                if store_mode.is_some() {
                    bail!("--play, --store-check and --store-reset are mutually exclusive");
                }
                store_mode = Some(if arg == "--store-check" {
                    Mode::StoreCheck
                } else {
                    Mode::StoreReset
                });
            }
            "--play" => video = Some(args.next().context("--play requires a path")?),
            "--status" => {
                status = Some(PathBuf::from(
                    args.next().context("--status requires a path")?,
                ))
            }
            _ => bail!(
                "unknown option {arg}; usage: wallpaper-spike --play <video> [--status <path>]"
            ),
        }
    }
    if let Some(mode) = store_mode {
        if video.is_some() || status.is_some() {
            bail!(
                "--play, --store-check and --store-reset are mutually exclusive; --status requires --play"
            );
        }
        return Ok(mode);
    }
    let Some(video) = video else {
        return Ok(Mode::Run { status });
    };
    let path = PathBuf::from(video);
    let path = if path.is_absolute() {
        path
    } else {
        std::env::current_dir()?.join(path)
    };
    Ok(Mode::Play(Options {
        video: path.to_str().context("video path is not UTF-8")?.into(),
        status,
    }))
}

fn parse_library(args: &[String]) -> Result<LibraryCommand> {
    use LibraryCommand::*;
    let strings: Vec<_> = args.iter().map(String::as_str).collect();
    Ok(match strings.as_slice() {
        ["--legacy-import", rest @ ..] => {
            let (mut from, mut library) = (None, None);
            let mut args = rest.iter();
            while let Some(arg) = args.next() {
                let slot = match *arg {
                    "--from" => &mut from,
                    "--from-library" => &mut library,
                    _ => bail!("invalid legacy import option: {arg}"),
                };
                anyhow::ensure!(slot.is_none(), "duplicate option: {arg}");
                let value = args
                    .next()
                    .context("legacy import option requires a directory")?;
                anyhow::ensure!(
                    !value.starts_with("--"),
                    "legacy import option requires a directory"
                );
                *slot = Some(PathBuf::from(value));
            }
            LegacyImport { from, library }
        }
        ["--import", paths @ ..] if !paths.is_empty() => {
            // Import consumes the tail as paths, but known CLI modes remain
            // mutually exclusive. Use ./--status for a file with that name.
            anyhow::ensure!(
                !paths.iter().any(|p| matches!(
                    *p,
                    "--play"
                        | "--status"
                        | "--store-check"
                        | "--store-reset"
                        | "--library-list"
                        | "--delete"
                        | "--assign"
                        | "--set-fit"
                        | "--displays"
                        | "--import"
                        | "--legacy-import"
                        | "--launch-at-login"
                        | "--export-diagnostics"
                        | "--from"
                        | "--from-library"
                )),
                "library modes are mutually exclusive"
            );
            Import(paths.iter().map(PathBuf::from).collect())
        }
        ["--library-list"] => List {
            query: String::new(),
        },
        ["--library-list", "--search", query] => List {
            query: (*query).into(),
        },
        ["--delete", id] => Delete {
            id: (*id).into(),
            yes: false,
        },
        ["--delete", id, "--yes"] => Delete {
            id: (*id).into(),
            yes: true,
        },
        ["--assign", id, "--all"] => Assign {
            id: (*id).into(),
            display: None,
        },
        ["--assign", id, "--display", display] => Assign {
            id: (*id).into(),
            display: Some((*display).into()),
        },
        ["--set-fit", id, value] => SetFit {
            id: (*id).into(),
            fit: match *value {
                "fill" => wallpaper_core::library::Fit::Fill,
                "fit" => wallpaper_core::library::Fit::Fit,
                "stretch" => wallpaper_core::library::Fit::Stretch,
                _ => bail!("fit must be fill, fit or stretch"),
            },
        },
        ["--displays"] => Displays,
        _ => bail!("invalid library command; modes are mutually exclusive"),
    })
}

pub(crate) fn run_library(
    command: LibraryCommand,
    paths: wallpaper_spike::storage::Paths,
) -> Result<i32> {
    use LibraryCommand::*;
    use wallpaper_spike::{import, library_ops};
    // Avoid any recovery writes for an unconfirmed delete.
    let report = wallpaper_spike::storage::Store::inspect(&paths);
    if report.has_errors() {
        eprintln!(
            "{}\n请使用 --store-check 检查，或 --store-reset 重置",
            serde_json::to_string_pretty(&report.summary())?
        );
        return Ok(2);
    }
    match command {
        Import(sources) => {
            let job = import::start(paths, std::env::current_exe()?, sources);
            drain_import(job)
        }
        LegacyImport { from, library } => {
            use wallpaper_spike::legacy_import::{self, LegacySource, Os};
            let mut source = if let Some(config) = from {
                LegacySource {
                    library: config.join("library"),
                    config,
                }
            } else {
                let os = if cfg!(windows) { Os::Windows } else { Os::Mac };
                let Some(source) = legacy_import::default_source(os, |name| std::env::var_os(name))
                else {
                    eprintln!("cannot determine legacy source directory");
                    return Ok(8);
                };
                source
            };
            if let Some(library) = library {
                source.library = library;
            }
            match drain_import(legacy_import::start(
                paths,
                std::env::current_exe()?,
                source,
            )) {
                Err(e) if e.downcast_ref::<legacy_import::SourceError>().is_some() => {
                    eprintln!("{e:#}");
                    Ok(8)
                }
                result => result,
            }
        }
        List { query } => {
            println!(
                "{}",
                serde_json::to_string(&library_ops::list(&paths, &query)?)?
            );
            Ok(0)
        }
        Delete { id, yes } => {
            if !yes {
                println!(
                    "{}",
                    serde_json::to_string(&library_ops::delete_preview(&paths, &id)?)?
                );
                eprintln!("请加 --yes 确认删除");
                return Ok(5);
            }
            library_ops::delete(&paths, &id)?;
            Ok(0)
        }
        Assign { id, display } => {
            if let Some(display) = display {
                let main =
                    wallpaper_platform::MainThread::new().context("must run on main thread")?;
                let connected = wallpaper_platform::displays(main)?;
                library_ops::assign_display(
                    &paths,
                    &id,
                    &wallpaper_core::displays::DisplayId(display),
                    &connected,
                )?;
            } else {
                library_ops::assign_all(&paths, &id)?;
            }
            Ok(0)
        }
        SetFit { id, fit } => {
            library_ops::set_fit(&paths, &id, fit)?;
            Ok(0)
        }
        Displays => {
            let main = wallpaper_platform::MainThread::new().context("must run on main thread")?;
            println!(
                "{}",
                serde_json::to_string(&wallpaper_platform::displays(main)?)?
            );
            Ok(0)
        }
    }
}

fn drain_import(job: wallpaper_spike::import::Job) -> Result<i32> {
    loop {
        if wallpaper_platform::exit_requested() {
            job.cancel.store(true, std::sync::atomic::Ordering::Release);
        }
        match job
            .events
            .recv_timeout(std::time::Duration::from_millis(50))
        {
            Ok(event) => println!("{}", serde_json::to_string(&event)?),
            Err(std::sync::mpsc::RecvTimeoutError::Timeout) => {}
            Err(std::sync::mpsc::RecvTimeoutError::Disconnected) => break,
        }
    }
    let summary = job.join()?;
    Ok(if summary.cancelled {
        7
    } else if summary.rejected > 0 {
        6
    } else {
        0
    })
}

pub(crate) fn media_probe(args: impl Iterator<Item = std::ffi::OsString>) -> i32 {
    use wallpaper_core::library::{Kind, RejectCode, Rejection};
    use wallpaper_spike::import::ProbeOutput;
    let args: Vec<_> = args.collect();
    let result = (|| {
        let invalid = || {
            Rejection::new(
                RejectCode::DecodeFailed,
                "usage: --media-probe <path> --kind <video|image> [--thumb <dst>]",
            )
        };
        if args.len() != 3 && args.len() != 5 {
            return Err(invalid());
        }
        if args[1] != "--kind" {
            return Err(invalid());
        }
        let kind = match args[2].to_str() {
            Some("video") => Kind::Video,
            Some("image") => Kind::Image,
            _ => return Err(invalid()),
        };
        let thumbnail = if args.len() == 5 {
            if args[3] != "--thumb" {
                return Err(invalid());
            }
            Some(std::path::Path::new(&args[4]))
        } else {
            None
        };
        wallpaper_platform::media::probe(std::path::Path::new(&args[0]), kind, thumbnail)
    })();
    let (reply, code) = match result {
        Ok(probe) => (ProbeOutput::Ok(probe), 0),
        Err(rejected) => (ProbeOutput::Rejected(rejected), 3),
    };
    match serde_json::to_string(&reply) {
        Ok(json) => println!("{json}"),
        Err(e) => {
            eprintln!("media probe JSON: {e}");
            return 1;
        }
    }
    code
}

/// Read-only entry point before Paths, store inspection or the singleton lock.
pub(crate) fn signals_probe() -> i32 {
    let result = (|| -> Result<()> {
        anyhow::ensure!(std::env::args_os().len() == 2, "usage: --signals-probe");
        let main = wallpaper_platform::MainThread::new().context("must run on main thread")?;
        let start = std::time::Instant::now();
        let power = wallpaper_platform::power(main);
        let locked = wallpaper_platform::session_locked(main);
        let asleep = wallpaper_platform::displays_asleep(main);
        // An unavailable screen list is represented as an empty list, with an
        // explicit error, rather than converting unknown observations to No.
        let (displays, error) = match wallpaper_platform::displays(main) {
            Ok(displays) => (displays, None),
            Err(error) => (vec![], Some(format!("{error:#}"))),
        };
        let visibility = wallpaper_platform::visibility(main, &displays);
        println!(
            "{}",
            serde_json::to_string(&serde_json::json!({
                "power": power, "locked": locked, "asleep": asleep,
                "displays": visibility, "elapsedMs": start.elapsed().as_secs_f64() * 1000.0,
                "displayError": error,
            }))?
        );
        Ok(())
    })();
    match result {
        Ok(()) => 0,
        Err(e) => {
            eprintln!("signals probe: {e:#}");
            1
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn args(values: &[&str]) -> Result<Mode> {
        parse(values.iter().map(|value| value.to_string()))
    }

    #[test]
    fn library_modes_and_exclusivity() {
        for values in [
            vec!["--import", "one.png", "雪 山.PNG"],
            vec!["--legacy-import"],
            vec![
                "--legacy-import",
                "--from",
                "old",
                "--from-library",
                "media",
            ],
            vec!["--legacy-import", "--from-library", "media"],
            vec!["--library-list"],
            vec!["--library-list", "--search", "äbc"],
            vec!["--delete", "one"],
            vec!["--delete", "one", "--yes"],
            vec!["--assign", "one", "--all"],
            vec!["--assign", "one", "--display", "screen"],
            vec!["--set-fit", "one", "stretch"],
            vec!["--displays"],
        ] {
            assert!(
                matches!(args(&values).unwrap(), Mode::Library(_)),
                "{values:?}"
            );
        }
        for values in [
            vec!["--import"],
            vec!["--legacy-import", "--from"],
            vec!["--legacy-import", "--from", "--from-library", "x"],
            vec!["--legacy-import", "--from", "x", "--from", "y"],
            vec!["--legacy-import", "--library-list"],
            vec!["--import", "x", "--legacy-import"],
            vec!["--import", "one.png", "--status", "state.json"],
            vec!["--library-list", "--play", "x"],
            vec!["--play", "x", "--library-list"],
            vec!["--store-check", "--delete", "x"],
            vec!["--assign", "x", "--all", "--display", "y"],
            vec!["--assign", "x"],
            vec!["--delete", "x", "--yes", "--yes"],
            vec!["--set-fit", "x", "bad"],
            vec!["--displays", "--status", "x"],
        ] {
            assert!(args(&values).is_err(), "{values:?}");
        }
    }
    #[test]
    fn no_play_means_run() {
        assert!(matches!(args(&[]).unwrap(), Mode::Run { status: None }));
        assert!(
            matches!(args(&["--status", "state.json"]).unwrap(), Mode::Run { status: Some(path) } if path == std::path::Path::new("state.json"))
        );
    }
    #[test]
    fn store_modes_are_exclusive() {
        assert!(matches!(
            args(&["--store-check"]).unwrap(),
            Mode::StoreCheck
        ));
        assert!(matches!(
            args(&["--store-reset"]).unwrap(),
            Mode::StoreReset
        ));
        for values in [
            vec!["--store-check", "--store-reset"],
            vec!["--store-reset", "--store-check"],
            vec!["--store-check", "--play", "synthetic.mp4"],
            vec!["--play", "synthetic.mp4", "--store-reset"],
            vec!["--store-check", "--status", "synthetic.json"],
        ] {
            assert!(args(&values).is_err());
        }
    }

    #[test]
    fn play_paths_and_existing_errors_unchanged() {
        let Mode::Play(options) =
            args(&["--play", "synthetic.mp4", "--status", "synthetic.json"]).unwrap()
        else {
            panic!("play expected")
        };
        assert_eq!(
            PathBuf::from(options.video),
            std::env::current_dir().unwrap().join("synthetic.mp4")
        );
        assert_eq!(options.status, Some(PathBuf::from("synthetic.json")));
        for (values, expected) in [
            (vec!["--play"], "--play requires a path"),
            (vec!["--status"], "--status requires a path"),
            (
                vec!["--unknown"],
                "unknown option --unknown; usage: wallpaper-spike --play <video> [--status <path>]",
            ),
        ] {
            assert_eq!(args(&values).err().unwrap().to_string(), expected);
        }
    }
}
