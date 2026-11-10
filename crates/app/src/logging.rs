use std::{
    fs::{self, File, OpenOptions},
    io::{self, Write},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
};
pub const RECORD_LIMIT: usize = 16 * 1024;
const FILE_LIMIT: u64 = 2 * 1024 * 1024;
#[derive(Default)]
struct Sink {
    file: Option<File>,
    path: PathBuf,
    size: u64,
}
#[derive(Clone, Default)]
pub struct Logger(Arc<Mutex<Sink>>);
impl Logger {
    pub fn install() -> Self {
        let logger = Self::default();
        env_logger::Builder::from_env(env_logger::Env::default().default_filter_or("info"))
            .write_style(env_logger::WriteStyle::Never)
            .target(env_logger::Target::Pipe(Box::new(logger.clone())))
            .init();
        logger
    }
    pub fn enable(&self, directory: &Path) {
        let mut sink = self.0.lock().unwrap_or_else(|e| e.into_inner());
        match (|| -> io::Result<()> {
            fs::create_dir_all(directory)?;
            sink.path = directory.join("app.log");
            // Do not follow a pre-existing log symlink.
            if fs::symlink_metadata(&sink.path).is_ok_and(|m| !m.is_file()) {
                return Err(io::Error::other("log is not a regular file"));
            }
            let file = OpenOptions::new()
                .create(true)
                .append(true)
                .open(&sink.path)?;
            sink.size = file.metadata()?.len();
            sink.file = Some(file);
            Ok(())
        })() {
            Ok(()) => {}
            Err(_) => {
                sink.file = None;
                let _ = writeln!(io::stderr(), "file logging unavailable; using stderr");
            }
        }
    }
}
fn record(bytes: &[u8]) -> Vec<u8> {
    // env_logger disables its own color; also strip escapes supplied by a caller.
    let text = String::from_utf8_lossy(bytes);
    let mut clean = String::new();
    let mut chars = text.chars();
    while let Some(c) = chars.next() {
        if c == '\u{1b}' {
            if chars.next() == Some('[') {
                for c in chars.by_ref() {
                    if ('@'..='~').contains(&c) {
                        break;
                    }
                }
            }
            continue;
        }
        clean.push(c);
    }
    if clean.len() > RECORD_LIMIT {
        let suffix = " [truncated]\n";
        let mut end = RECORD_LIMIT - suffix.len();
        while !clean.is_char_boundary(end) {
            end -= 1;
        }
        clean.truncate(end);
        clean.push_str(suffix);
    }
    clean.into_bytes()
}
impl Sink {
    fn append(&mut self, bytes: &[u8]) -> io::Result<()> {
        if self.file.is_none() {
            return Ok(());
        }
        if self.size + bytes.len() as u64 > FILE_LIMIT {
            self.file.take();
            let backup = self.path.with_file_name("app.log.1");
            match fs::remove_file(&backup) {
                Ok(()) => {}
                Err(e) if e.kind() == io::ErrorKind::NotFound => {}
                Err(e) => return Err(e),
            }
            fs::rename(&self.path, backup)?;
            self.file = Some(
                OpenOptions::new()
                    .create_new(true)
                    .write(true)
                    .open(&self.path)?,
            );
            self.size = 0;
        }
        self.file.as_mut().unwrap().write_all(bytes)?;
        self.size += bytes.len() as u64;
        Ok(())
    }
}
impl Write for Logger {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        let clean = record(bytes);
        let _ = io::stderr().write_all(&clean);
        let mut sink = self.0.lock().unwrap_or_else(|e| e.into_inner());
        if sink.append(&clean).is_err() {
            sink.file = None;
            let _ = writeln!(io::stderr(), "file logging failed; using stderr");
        }
        Ok(bytes.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn bounded_plain_record_and_rotation() {
        let bytes = record(format!("\x1b[31m{}\x1b[0m", "雪".repeat(RECORD_LIMIT)).as_bytes());
        assert!(bytes.len() <= RECORD_LIMIT);
        assert!(!bytes.contains(&27));
        assert!(bytes.ends_with(b"[truncated]\n"));
        let root = crate::test_temp("logs");
        let logger = Logger::default();
        logger.enable(&root);
        let mut sink = logger.0.lock().unwrap();
        sink.append(&vec![b'x'; FILE_LIMIT as usize]).unwrap();
        sink.append(b"next\n").unwrap();
        assert_eq!(fs::read(root.join("app.log")).unwrap(), b"next\n");
        assert_eq!(
            fs::metadata(root.join("app.log.1")).unwrap().len(),
            FILE_LIMIT
        );
        sink.append(&vec![b'y'; FILE_LIMIT as usize]).unwrap();
        assert_eq!(fs::read(root.join("app.log.1")).unwrap(), b"next\n");
    }
    #[test]
    fn unavailable_and_failed_sink_fall_back_without_recursion() {
        let root = crate::test_temp("log-failure");
        fs::create_dir_all(&root).unwrap();
        let blocked = root.join("blocked");
        fs::write(&blocked, b"file").unwrap();
        let mut logger = Logger::default();
        logger.enable(&blocked);
        logger.write_all(b"fallback\n").unwrap();
        assert!(logger.0.lock().unwrap().file.is_none());
        logger.enable(&root);
        {
            let mut sink = logger.0.lock().unwrap();
            sink.size = FILE_LIMIT;
            fs::create_dir(root.join("app.log.1")).unwrap();
        }
        logger.write_all(b"rotation failure\n").unwrap();
        logger.write_all(b"still stderr\n").unwrap();
        assert!(logger.0.lock().unwrap().file.is_none());
    }
}
