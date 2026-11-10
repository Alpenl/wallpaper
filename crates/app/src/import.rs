use crate::{
    library_ops::{open, real_directory, remove_directory, safe_id},
    storage::{Paths, Store},
};
use anyhow::{Context, Result};
use serde::{Deserialize, Serialize};
use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{
        Arc,
        atomic::{AtomicBool, AtomicU64, Ordering},
        mpsc::{self, Receiver, Sender},
    },
    thread::{self, JoinHandle},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
use wallpaper_core::library::{Fit, Item, Kind, Library, Probe, RejectCode, Rejection};

#[derive(Debug, Serialize, Deserialize)]
#[serde(tag = "event", rename_all = "snake_case")]
pub enum Event {
    Note {
        message: String,
    },
    Started {
        index: usize,
        total: usize,
        path: PathBuf,
    },
    Copying {
        index: usize,
        done: u64,
        total: u64,
    },
    Probing {
        index: usize,
    },
    Imported {
        index: usize,
        id: String,
        title: String,
    },
    Rejected {
        index: usize,
        path: PathBuf,
        code: RejectCode,
        detail: String,
    },
    Finished {
        imported: usize,
        rejected: usize,
        cancelled: bool,
    },
}
#[derive(Debug, Default, PartialEq, Eq)]
pub struct Summary {
    pub imported: usize,
    pub rejected: usize,
    pub cancelled: bool,
}
pub struct Job {
    pub cancel: Arc<AtomicBool>,
    pub events: Receiver<Event>,
    handle: JoinHandle<Result<Summary>>,
}
impl Job {
    pub fn join(self) -> Result<Summary> {
        self.handle
            .join()
            .map_err(|_| anyhow::anyhow!("import thread panicked"))?
    }
}
pub(crate) fn spawn(
    run: impl FnOnce(&AtomicBool, &Sender<Event>) -> Result<Summary> + Send + 'static,
) -> Job {
    let cancel = Arc::new(AtomicBool::new(false));
    let flag = cancel.clone();
    let (send, events) = mpsc::channel();
    let handle = thread::spawn(move || {
        let result = run(&flag, &send);
        finished(&result, &flag, &send);
        result
    });
    Job {
        cancel,
        events,
        handle,
    }
}
fn finished(result: &Result<Summary>, cancel: &AtomicBool, send: &Sender<Event>) {
    let summary = result.as_ref().ok();
    let _ = send.send(Event::Finished {
        imported: summary.map_or(0, |s| s.imported),
        rejected: summary.map_or(0, |s| s.rejected),
        cancelled: summary.map_or_else(|| cancel.load(Ordering::Acquire), |s| s.cancelled),
    });
}
#[derive(Default)]
pub(crate) struct Spec {
    pub id: Option<String>,
    pub title: Option<String>,
    pub fit: Fit,
    pub added_at: Option<String>,
}
pub fn start(paths: Paths, helper: PathBuf, sources: Vec<PathBuf>) -> Job {
    start_with_options(paths, helper, sources, Options::default())
}
pub(crate) struct Options {
    timeout: Duration,
    #[cfg(test)]
    copy_hook: Option<fn(&AtomicBool, u64, &Path)>,
    #[cfg(test)]
    commit_hook: Option<fn(&AtomicBool)>,
}
impl Default for Options {
    fn default() -> Self {
        Self {
            timeout: Duration::from_secs(30),
            #[cfg(test)]
            copy_hook: None,
            #[cfg(test)]
            commit_hook: None,
        }
    }
}
fn start_with_options(
    paths: Paths,
    helper: PathBuf,
    sources: Vec<PathBuf>,
    options: Options,
) -> Job {
    spawn(move |cancel, send| run(paths, helper, sources, cancel, send, &options))
}
fn run(
    paths: Paths,
    helper: PathBuf,
    sources: Vec<PathBuf>,
    cancel: &AtomicBool,
    send: &Sender<Event>,
    options: &Options,
) -> Result<Summary> {
    let report = open(&paths)?;
    let mut library = report.library.value.context("missing library")?;
    prepare(&paths)?;
    let mut summary = Summary::default();
    for (index, source) in sources.iter().enumerate() {
        if cancel.load(Ordering::Acquire) {
            summary.cancelled = true;
            break;
        }
        let _ = send.send(Event::Started {
            index,
            total: sources.len(),
            path: source.clone(),
        });
        match import_one(
            &paths,
            &helper,
            source,
            &mut library,
            cancel,
            send,
            index,
            options,
        ) {
            Ok(Some(item)) => {
                summary.imported += 1;
                let _ = send.send(Event::Imported {
                    index,
                    id: item.id,
                    title: item.title,
                });
            }
            Ok(None) => {
                summary.cancelled = true;
                break;
            }
            Err(rejection) => {
                summary.rejected += 1;
                let stop = rejection.code == RejectCode::StorageFailed;
                let _ = send.send(Event::Rejected {
                    index,
                    path: source.clone(),
                    code: rejection.code,
                    detail: rejection.detail,
                });
                if stop {
                    break;
                }
            }
        }
    }
    summary.cancelled |= cancel.load(Ordering::Acquire);
    Ok(summary)
}
pub(crate) fn prepare(paths: &Paths) -> Result<()> {
    real_directory(&paths.library, true)?;
    for entry in fs::read_dir(&paths.library)? {
        let entry = entry?;
        if entry.file_name().to_string_lossy().starts_with(".staging-")
            && entry.file_type()?.is_dir()
        {
            // Only our generated, safe suffixes are eligible for cleanup.
            let name = entry.file_name();
            if let Some(id) = name.to_str().and_then(|s| s.strip_prefix(".staging-"))
                && safe_id(id).is_ok()
            {
                remove_directory(&entry.path())?;
            }
        }
    }
    Ok(())
}
fn extension(path: &Path) -> Option<(String, Kind)> {
    let ext = path.extension()?.to_str()?.to_lowercase();
    let kind = match ext.as_str() {
        "mp4" | "mov" | "m4v" | "webm" => Kind::Video,
        "gif" | "png" | "jpg" | "jpeg" | "webp" | "bmp" => Kind::Image,
        _ => return None,
    };
    Some((ext, kind))
}
fn new_id(paths: &Paths, library: &Library) -> Result<String> {
    static SERIAL: AtomicU64 = AtomicU64::new(0);
    loop {
        let nonce = SystemTime::now().duration_since(UNIX_EPOCH)?.as_nanos();
        let value = (nonce << 32)
            ^ ((std::process::id() as u128) << 64)
            ^ SERIAL.fetch_add(1, Ordering::Relaxed) as u128;
        let id = format!("{value:032x}");
        safe_id(&id)?;
        if library.find(&id).is_some() {
            continue;
        }
        let mut collision = false;
        for root in [&paths.library, &paths.cache] {
            // An unavailable cache never prevents importing the media itself.
            if root == &paths.cache && real_directory(root, false).is_err() {
                continue;
            }
            match fs::symlink_metadata(root.join(&id)) {
                Ok(_) => collision = true,
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
                Err(e) if root == &paths.cache => {
                    eprintln!("thumbnail directory: {e}");
                }
                Err(e) => return Err(e.into()),
            }
        }
        match fs::symlink_metadata(paths.library.join(format!(".staging-{id}"))) {
            Ok(_) => collision = true,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(e.into()),
        }
        if !collision {
            return Ok(id);
        }
    }
}
struct Staging(PathBuf);
impl Drop for Staging {
    fn drop(&mut self) {
        if let Err(e) = remove_directory(&self.0) {
            eprintln!("cleanup {}: {e:#}", self.0.display());
        }
    }
}
#[allow(clippy::too_many_arguments)]
fn import_one(
    paths: &Paths,
    helper: &Path,
    source: &Path,
    library: &mut Library,
    cancel: &AtomicBool,
    send: &Sender<Event>,
    index: usize,
    options: &Options,
) -> std::result::Result<Option<Item>, Rejection> {
    import_with_spec(
        paths,
        helper,
        source,
        library,
        cancel,
        send,
        index,
        &Spec::default(),
        options,
    )
}
#[allow(clippy::too_many_arguments)]
pub(crate) fn import_with_spec(
    paths: &Paths,
    helper: &Path,
    source: &Path,
    library: &mut Library,
    cancel: &AtomicBool,
    send: &Sender<Event>,
    index: usize,
    spec: &Spec,
    options: &Options,
) -> std::result::Result<Option<Item>, Rejection> {
    let reject = |code, e: std::io::Error| Rejection::new(code, e);
    let meta = fs::symlink_metadata(source).map_err(|e| reject(RejectCode::NotAFile, e))?;
    let meta = if meta.file_type().is_symlink() {
        fs::metadata(source).map_err(|e| reject(RejectCode::NotAFile, e))?
    } else {
        meta
    };
    if !meta.is_file() {
        return Err(Rejection::new(
            RejectCode::NotAFile,
            "source is not a regular file",
        ));
    }
    if meta.len() == 0 {
        return Err(Rejection::new(RejectCode::EmptyFile, "source is empty"));
    }
    let (ext, kind) = extension(source).ok_or_else(|| {
        Rejection::new(
            RejectCode::UnsupportedExtension,
            "unsupported media extension",
        )
    })?;
    let id = if let Some(id) = &spec.id {
        safe_id(id).map_err(|e| Rejection::new(RejectCode::InvalidEntry, e))?;
        id.clone()
    } else {
        new_id(paths, library).map_err(|e| Rejection::new(RejectCode::StorageFailed, e))?
    };
    let stage = paths.library.join(format!(".staging-{id}"));
    fs::create_dir(&stage).map_err(|e| reject(RejectCode::CopyFailed, e))?;
    let staging = Staging(stage);
    let media_name = format!("media.{ext}");
    let media = staging.0.join(&media_name);
    let mut input = fs::File::open(source).map_err(|e| reject(RejectCode::CopyFailed, e))?;
    let opened = input
        .metadata()
        .map_err(|e| reject(RejectCode::CopyFailed, e))?;
    if !opened.is_file() {
        return Err(Rejection::new(
            RejectCode::NotAFile,
            "opened source is not a regular file",
        ));
    }
    let mut output = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(&media)
        .map_err(|e| reject(RejectCode::CopyFailed, e))?;
    let mut buffer = vec![0; 1024 * 1024];
    let mut copied = 0;
    let mut reported = 0;
    let mut last = Instant::now();
    loop {
        if cancel.load(Ordering::Acquire) {
            return Ok(None);
        }
        let n = input
            .read(&mut buffer)
            .map_err(|e| reject(RejectCode::CopyFailed, e))?;
        if n == 0 {
            break;
        }
        output
            .write_all(&buffer[..n])
            .map_err(|e| reject(RejectCode::CopyFailed, e))?;
        copied += n as u64;
        #[cfg(test)]
        if let Some(hook) = options.copy_hook {
            hook(cancel, copied, source);
        }
        if copied - reported >= 64 * 1024 * 1024 || last.elapsed() >= Duration::from_millis(500) {
            let _ = send.send(Event::Copying {
                index,
                done: copied,
                total: meta.len(),
            });
            reported = copied;
            last = Instant::now();
        }
    }
    output
        .sync_all()
        .map_err(|e| reject(RejectCode::CopyFailed, e))?;
    let actual = output
        .metadata()
        .map_err(|e| reject(RejectCode::CopyFailed, e))?
        .len();
    drop(output);
    drop(input);
    if copied != meta.len() || actual != copied {
        return Err(Rejection::new(
            RejectCode::CopyFailed,
            "source size changed during copy",
        ));
    }
    if cancel.load(Ordering::Acquire) {
        return Ok(None);
    }
    let _ = send.send(Event::Probing { index });
    let Some(probe) = probe_child(helper, &media, kind, &staging.0, cancel, options.timeout)?
    else {
        return Ok(None);
    };
    let item = Item {
        id: id.clone(),
        title: spec
            .title
            .clone()
            .filter(|s| !s.is_empty())
            .unwrap_or_else(|| {
                source
                    .file_stem()
                    .unwrap_or_default()
                    .to_string_lossy()
                    .into_owned()
            }),
        kind,
        entry: media_name,
        width: Some(probe.width),
        height: Some(probe.height),
        duration_sec: probe.duration_sec,
        size_bytes: Some(actual),
        added_at: spec
            .added_at
            .clone()
            .filter(|s| !s.is_empty())
            .unwrap_or_else(wallpaper_core::timestamp),
        fit: spec.fit,
    };
    // Cancellation is honored through this point. The following commit is indivisible
    // with respect to cancellation; a successfully saved item is always reported.
    if cancel.load(Ordering::Acquire) {
        return Ok(None);
    }
    let cache = paths.cache.join(&id);
    if staging.0.join("thumb.jpg").is_file() {
        let moved = (|| -> Result<()> {
            real_directory(&paths.cache, true)?;
            real_directory(&cache, true)?;
            fs::rename(staging.0.join("thumb.jpg"), cache.join("thumb.jpg"))?;
            Ok(())
        })();
        if let Err(e) = moved {
            eprintln!("thumbnail: {e:#}");
        }
    }
    let final_dir = paths.library.join(&id);
    if let Err(e) = fs::rename(&staging.0, &final_dir) {
        let _ = remove_directory(&cache);
        return Err(reject(RejectCode::StorageFailed, e));
    }
    library.items.push(item.clone());
    if let Err(e) = Store::save_library(paths, library) {
        library.items.pop();
        let media_cleanup = remove_directory(&final_dir);
        let cache_cleanup = remove_directory(&cache);
        return Err(Rejection::new(
            RejectCode::StorageFailed,
            format!("{e:#}; rollback media: {media_cleanup:?}; cache: {cache_cleanup:?}"),
        ));
    }
    #[cfg(test)]
    if let Some(hook) = options.commit_hook {
        hook(cancel);
    }
    Ok(Some(item))
}
#[derive(Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum ProbeOutput {
    Ok(Probe),
    Rejected(Rejection),
}
fn probe_child(
    helper: &Path,
    media: &Path,
    kind: Kind,
    stage: &Path,
    cancel: &AtomicBool,
    timeout: Duration,
) -> std::result::Result<Option<Probe>, Rejection> {
    let crashed = |e: &dyn std::fmt::Display| Rejection::new(RejectCode::Crashed, e);
    let stdout = stage.join("probe.json");
    let file = fs::File::create(&stdout).map_err(|e| crashed(&e))?;
    let mut child = Command::new(helper)
        .arg("--media-probe")
        .arg(media)
        .arg("--kind")
        .arg(match kind {
            Kind::Image => "image",
            Kind::Video => "video",
        })
        .arg("--thumb")
        .arg(stage.join("thumb.jpg"))
        .stdout(Stdio::from(file))
        .stderr(Stdio::inherit())
        .stdin(Stdio::null())
        .spawn()
        .map_err(|e| crashed(&e))?;
    let start = Instant::now();
    let status = loop {
        if cancel.load(Ordering::Acquire) || start.elapsed() >= timeout {
            let _ = child.kill();
            child.wait().map_err(|e| crashed(&e))?;
            if cancel.load(Ordering::Acquire) {
                return Ok(None);
            }
            return Err(Rejection::new(RejectCode::Timeout, "media probe timed out"));
        }
        match child.try_wait() {
            Ok(Some(status)) => break status,
            Ok(None) => thread::sleep(Duration::from_millis(50)),
            Err(e) => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(crashed(&e));
            }
        }
    };
    if !matches!(status.code(), Some(0 | 3)) {
        return Err(crashed(&format!("media probe exited with {status}")));
    }
    let mut bytes = Vec::new();
    fs::File::open(&stdout)
        .and_then(|f| f.take(65537).read_to_end(&mut bytes))
        .map_err(|e| crashed(&e))?;
    if bytes.len() > 65536 {
        return Err(crashed(&"media probe output exceeds 64 KiB"));
    }
    let reply: ProbeOutput = serde_json::from_slice(&bytes).map_err(|e| crashed(&e))?;
    fs::remove_file(&stdout).map_err(|e| crashed(&e))?;
    match (status.code(), reply) {
        (Some(0), ProbeOutput::Ok(probe))
            if probe.kind == kind
                && probe.width > 0
                && probe.height > 0
                && probe.duration_sec.is_none_or(|d| d.is_finite() && d >= 0.) =>
        {
            Ok(Some(probe))
        }
        (Some(3), ProbeOutput::Rejected(rejection)) => Err(rejection),
        _ => Err(crashed(&"inconsistent media probe response")),
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::os::unix::fs::{PermissionsExt, symlink};
    struct Fixture {
        root: PathBuf,
        paths: Paths,
    }
    impl Fixture {
        fn new() -> Self {
            static SERIAL: AtomicU64 = AtomicU64::new(0);
            let root = std::env::temp_dir().join(format!(
                "wp-import-{}-{}",
                std::process::id(),
                SERIAL.fetch_add(1, Ordering::Relaxed)
            ));
            fs::create_dir(&root).unwrap();
            let paths = Paths {
                config: root.join("config"),
                library: root.join("library"),
                cache: root.join("cache"),
                logs: root.join("logs"),
            };
            Self { root, paths }
        }
        fn source(&self, name: &str) -> PathBuf {
            let p = self.root.join(name);
            fs::write(&p, b"media bytes").unwrap();
            p
        }
        fn helper(&self, script: &str) -> PathBuf {
            let p = self.root.join("helper.sh");
            fs::write(&p, format!("#!/bin/sh\n{script}\n")).unwrap();
            fs::set_permissions(&p, fs::Permissions::from_mode(0o700)).unwrap();
            p
        }
        fn run(
            &self,
            script: &str,
            sources: Vec<PathBuf>,
            options: Options,
        ) -> (Summary, Vec<Event>) {
            let job = start_with_options(self.paths.clone(), self.helper(script), sources, options);
            let events: Vec<_> = job.events.iter().collect();
            (job.join().unwrap(), events)
        }
        fn clean(&self) {
            assert!(fs::read_dir(&self.paths.library).unwrap().next().is_none());
            assert!(
                Store::inspect(&self.paths)
                    .library
                    .value
                    .unwrap()
                    .items
                    .is_empty()
            );
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.root);
        }
    }
    const SUCCESS: &str = "printf '%s\\n' '{\"ok\":{\"kind\":\"image\",\"width\":32,\"height\":16,\"durationSec\":null}}'";
    #[test]
    fn successful_unicode_space_uppercase_import_and_thumbnail() {
        let f = Fixture::new();
        let source = f.source("雪 山 Ä.PNG");
        let (s, events) = f.run(
            &format!("printf jpeg > \"$6\"\n{SUCCESS}"),
            vec![source],
            Options::default(),
        );
        assert_eq!(
            s,
            Summary {
                imported: 1,
                rejected: 0,
                cancelled: false
            }
        );
        let report = Store::inspect(&f.paths);
        let item = &report.library.value.unwrap().items[0];
        assert_eq!(item.title, "雪 山 Ä");
        assert_eq!(item.entry, "media.png");
        assert_eq!(item.size_bytes, Some(11));
        assert_eq!(item.fit, Fit::Fill);
        assert_eq!(item.id.len(), 32);
        assert!(item.id.bytes().all(|c| c.is_ascii_hexdigit()));
        assert!(item.added_at.ends_with('Z'));
        assert_eq!(
            fs::read(f.paths.library.join(&item.id).join(&item.entry)).unwrap(),
            b"media bytes"
        );
        assert!(f.paths.cache.join(&item.id).join("thumb.jpg").is_file());
        assert!(!f.paths.library.join(&item.id).join("probe.json").exists());
        assert!(matches!(
            events.last(),
            Some(Event::Finished { imported: 1, .. })
        ));
    }
    #[test]
    fn missing_thumbnail_is_success() {
        let f = Fixture::new();
        assert_eq!(
            f.run(SUCCESS, vec![f.source("x.png")], Options::default())
                .0
                .imported,
            1
        );
        assert!(
            crate::library_ops::list(&f.paths, "").unwrap()[0]
                .thumbnail
                .is_none()
        );
    }
    #[test]
    fn rejection_crash_garbage_oversized_and_inconsistent_stdout_leave_no_residue() {
        for (script, code) in [
            (
                "echo '{\"rejected\":{\"code\":\"animated_image\",\"detail\":\"v1 unsupported\"}}'; exit 3",
                RejectCode::AnimatedImage,
            ),
            ("exit 9", RejectCode::Crashed),
            ("echo garbage", RejectCode::Crashed),
            (
                "dd if=/dev/zero bs=65537 count=1 2>/dev/null",
                RejectCode::Crashed,
            ),
            (
                "echo '{\"rejected\":{\"code\":\"decode_failed\",\"detail\":\"bad\"}}'",
                RejectCode::Crashed,
            ),
            (
                "echo '{\"ok\":{\"kind\":\"video\",\"width\":1,\"height\":1,\"durationSec\":null}}'",
                RejectCode::Crashed,
            ),
        ] {
            let f = Fixture::new();
            let (s, events) = f.run(script, vec![f.source("x.gif")], Options::default());
            assert_eq!(s.rejected, 1);
            assert!(
                events
                    .iter()
                    .any(|e| matches!(e, Event::Rejected { code: c, .. } if *c == code))
            );
            f.clean();
        }
    }
    #[test]
    fn timeout_kills_and_reaps_helper() {
        let f = Fixture::new();
        let (s, events) = f.run(
            "echo $$ > \"$(dirname \"$2\")/pid\"\nexec sleep 30",
            vec![f.source("x.png")],
            Options {
                timeout: Duration::from_millis(150),
                ..Options::default()
            },
        );
        assert_eq!(s.rejected, 1);
        assert!(events.iter().any(|e| matches!(
            e,
            Event::Rejected {
                code: RejectCode::Timeout,
                ..
            }
        )));
        f.clean();
    }
    #[test]
    fn cancellation_kills_and_reaps_hanging_helper() {
        let f = Fixture::new();
        let helper = f.helper("echo $$ > \"$(dirname \"$2\")/pid\"\nexec sleep 30");
        let job = start(f.paths.clone(), helper, vec![f.source("x.png")]);
        let deadline = Instant::now() + Duration::from_secs(5);
        let pid = loop {
            let pid = fs::read_dir(&f.paths.library).ok().and_then(|entries| {
                entries
                    .filter_map(|e| e.ok())
                    .find_map(|e| fs::read_to_string(e.path().join("pid")).ok())
            });
            if let Some(pid) = pid {
                break pid;
            }
            assert!(Instant::now() < deadline);
            thread::sleep(Duration::from_millis(10));
        };
        job.cancel.store(true, Ordering::Release);
        let events: Vec<_> = job.events.iter().collect();
        assert!(job.join().unwrap().cancelled);
        assert!(matches!(
            events.last(),
            Some(Event::Finished {
                cancelled: true,
                ..
            })
        ));
        let status = Command::new("/bin/kill")
            .arg("-0")
            .arg(pid.trim())
            .stderr(Stdio::null())
            .status()
            .unwrap();
        assert!(!status.success(), "helper still alive");
        f.clean();
    }
    #[test]
    fn cancellation_during_copy_removes_staging() {
        let f = Fixture::new();
        let source = f.source("big.png");
        fs::OpenOptions::new()
            .write(true)
            .open(&source)
            .unwrap()
            .set_len(4 * 1024 * 1024)
            .unwrap();
        let (s, events) = f.run(
            SUCCESS,
            vec![source],
            Options {
                copy_hook: Some(|cancel, _, _| cancel.store(true, Ordering::Release)),
                ..Options::default()
            },
        );
        assert!(s.cancelled);
        assert!(!events.iter().any(|e| matches!(e, Event::Probing { .. })));
        f.clean();
    }
    #[test]
    fn source_truncated_during_copy_is_copy_failed_without_residue() {
        let f = Fixture::new();
        let source = f.source("big.png");
        fs::OpenOptions::new()
            .write(true)
            .open(&source)
            .unwrap()
            .set_len(4 * 1024 * 1024)
            .unwrap();
        let (summary, events) = f.run(
            SUCCESS,
            vec![source],
            Options {
                copy_hook: Some(|_, _, path| {
                    fs::OpenOptions::new()
                        .write(true)
                        .open(path)
                        .unwrap()
                        .set_len(0)
                        .unwrap();
                }),
                ..Options::default()
            },
        );
        assert_eq!(summary.rejected, 1);
        assert!(events.iter().any(|event| matches!(
            event,
            Event::Rejected {
                code: RejectCode::CopyFailed,
                ..
            }
        )));
        f.clean();
    }
    #[test]
    fn cancellation_after_commit_preserves_item_and_stops_batch() {
        let f = Fixture::new();
        let (s, _) = f.run(
            SUCCESS,
            vec![f.source("one.png"), f.source("two.png")],
            Options {
                commit_hook: Some(|cancel| cancel.store(true, Ordering::Release)),
                ..Options::default()
            },
        );
        assert_eq!(
            s,
            Summary {
                imported: 1,
                rejected: 0,
                cancelled: true
            }
        );
        assert_eq!(
            Store::inspect(&f.paths).library.value.unwrap().items.len(),
            1
        );
        assert_eq!(fs::read_dir(&f.paths.library).unwrap().count(), 1);
    }
    #[test]
    fn batch_continues_after_rejection_and_supports_symlink_file() {
        let f = Fixture::new();
        let source = f.source("one.png");
        let link = f.root.join("linked.PNG");
        symlink(&source, &link).unwrap();
        let (s, _) = f.run(
            SUCCESS,
            vec![source, f.source("bad.txt"), link],
            Options::default(),
        );
        assert_eq!(s.imported, 2);
        assert_eq!(s.rejected, 1);
        let items = Store::inspect(&f.paths).library.value.unwrap().items;
        assert_ne!(items[0].id, items[1].id);
    }
    #[test]
    fn invalid_sources_and_extensions() {
        let f = Fixture::new();
        let empty = f.root.join("empty.png");
        fs::write(&empty, []).unwrap();
        let link = f.root.join("dir.png");
        symlink(&f.paths.config, &link).unwrap();
        fs::create_dir(&f.paths.config).unwrap();
        let (s, events) = f.run(
            SUCCESS,
            vec![
                empty,
                link,
                f.source("unknown.xyz"),
                f.root.join("missing.png"),
                f.paths.config.clone(),
            ],
            Options::default(),
        );
        assert_eq!(s.rejected, 5);
        let codes: Vec<_> = events
            .iter()
            .filter_map(|e| {
                if let Event::Rejected { code, .. } = e {
                    Some(*code)
                } else {
                    None
                }
            })
            .collect();
        assert_eq!(
            codes,
            [
                RejectCode::EmptyFile,
                RejectCode::NotAFile,
                RejectCode::UnsupportedExtension,
                RejectCode::NotAFile,
                RejectCode::NotAFile
            ]
        );
        f.clean();
        for ext in [
            "mp4", "mov", "m4v", "webm", "gif", "png", "jpg", "jpeg", "webp", "bmp",
        ] {
            assert!(extension(Path::new(&format!("x.{ext}"))).is_some());
        }
        for ext in ["tiff", "wpk", "zip", "glsl", "frag", "mp3"] {
            assert!(extension(Path::new(&format!("x.{ext}"))).is_none());
        }
    }
    #[test]
    fn library_save_failure_rolls_back_and_stops_batch() {
        let f = Fixture::new();
        fs::create_dir(&f.paths.config).unwrap();
        // Helper runs after Store::open; create the write obstacle at that point.
        let script = format!(
            "mkdir '{}'\nprintf jpeg > \"$6\"\n{SUCCESS}",
            f.paths.config.join("library.json").display()
        );
        let (s, events) = f.run(
            &script,
            vec![f.source("one.png"), f.source("two.png")],
            Options::default(),
        );
        assert_eq!(s.rejected, 1);
        assert_eq!(s.imported, 0);
        assert_eq!(
            events
                .iter()
                .filter(|e| matches!(e, Event::Started { .. }))
                .count(),
            1
        );
        assert!(events.iter().any(|e| matches!(
            e,
            Event::Rejected {
                code: RejectCode::StorageFailed,
                ..
            }
        )));
        assert_eq!(fs::read_dir(&f.paths.library).unwrap().count(), 0);
        assert_eq!(fs::read_dir(&f.paths.cache).unwrap().count(), 0);
    }
    #[test]
    fn corrupt_store_rejected_without_changes_or_staging_cleanup() {
        let f = Fixture::new();
        fs::create_dir(&f.paths.config).unwrap();
        fs::write(f.paths.config.join("library.json"), b"broken").unwrap();
        fs::create_dir_all(f.paths.library.join(".staging-old")).unwrap();
        let job = start(
            f.paths.clone(),
            f.helper(SUCCESS),
            vec![f.source("one.png")],
        );
        assert!(job.join().is_err());
        assert_eq!(
            fs::read(f.paths.config.join("library.json")).unwrap(),
            b"broken"
        );
        assert_eq!(fs::read_dir(&f.paths.config).unwrap().count(), 1);
        assert!(f.paths.library.join(".staging-old").is_dir());
    }
    #[test]
    fn stale_staging_cleanup_does_not_follow_links_or_collect_orphans() {
        let f = Fixture::new();
        fs::create_dir_all(f.paths.library.join(".staging-old")).unwrap();
        fs::create_dir(f.paths.library.join("orphan")).unwrap();
        fs::write(f.paths.library.join(".staging-file"), b"preserve").unwrap();
        let external = f.root.join("external");
        fs::create_dir(&external).unwrap();
        fs::write(external.join("keep"), b"keep").unwrap();
        symlink(&external, f.paths.library.join(".staging-link")).unwrap();
        f.run(SUCCESS, vec![], Options::default());
        assert!(!f.paths.library.join(".staging-old").exists());
        assert!(f.paths.library.join(".staging-link").is_symlink());
        assert!(external.join("keep").is_file());
        assert!(f.paths.library.join("orphan").is_dir());
        assert!(f.paths.library.join(".staging-file").is_file());
    }
    #[test]
    fn library_and_cache_symlinks_cannot_escape() {
        for cache_link in [false, true] {
            let f = Fixture::new();
            let external = f.root.join("external");
            fs::create_dir(&external).unwrap();
            symlink(
                &external,
                if cache_link {
                    &f.paths.cache
                } else {
                    &f.paths.library
                },
            )
            .unwrap();
            let job = start(
                f.paths.clone(),
                f.helper(&format!("printf jpeg > \"$6\"\n{SUCCESS}")),
                vec![f.source("one.png")],
            );
            let result = job.join();
            if cache_link {
                assert_eq!(result.unwrap().imported, 1);
            } else {
                assert!(result.is_err());
            }
            assert_eq!(fs::read_dir(external).unwrap().count(), 0);
        }
    }
}
