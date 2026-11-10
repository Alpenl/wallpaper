use super::{Handle, wide};
use anyhow::{Result, bail};
use serde::{Serialize, de::DeserializeOwned};
use windows::{
    Win32::{Foundation::*, Storage::FileSystem::*, System::Pipes::*},
    core::PCWSTR,
};

// Message mode + NOWAIT keeps the GPUI thread and worker render loop bounded.
// At most one small request/response is outstanding; no background IPC threads.
pub struct Pipe {
    handle: Handle,
    connected: bool,
}
impl Pipe {
    pub fn server(name: &str) -> Result<Self> {
        let name = wide(name);
        let h = unsafe {
            CreateNamedPipeW(
                PCWSTR(name.as_ptr()),
                PIPE_ACCESS_DUPLEX | FILE_FLAG_FIRST_PIPE_INSTANCE,
                PIPE_TYPE_MESSAGE
                    | PIPE_READMODE_MESSAGE
                    | PIPE_NOWAIT
                    | PIPE_REJECT_REMOTE_CLIENTS,
                1,
                16384,
                16384,
                0,
                None,
            )
        };
        Ok(Self {
            handle: Handle::new(h)?,
            connected: false,
        })
    }
    pub fn client(name: &str) -> Result<Self> {
        let name = wide(name);
        let handle = Handle::new(unsafe {
            CreateFileW(
                PCWSTR(name.as_ptr()),
                GENERIC_READ.0 | GENERIC_WRITE.0,
                FILE_SHARE_MODE(0),
                None,
                OPEN_EXISTING,
                FILE_FLAGS_AND_ATTRIBUTES(0),
                None,
            )?
        })?;
        unsafe {
            SetNamedPipeHandleState(
                handle.0,
                Some(&(PIPE_READMODE_MESSAGE | PIPE_NOWAIT)),
                None,
                None,
            )?;
        }
        Ok(Self {
            handle,
            connected: true,
        })
    }
    pub fn connect(&mut self, pid: u32) -> Result<bool> {
        if self.connected {
            return Ok(true);
        }
        unsafe {
            if let Err(e) = ConnectNamedPipe(self.handle.0, None) {
                if e.code() == ERROR_PIPE_LISTENING.to_hresult() {
                    return Ok(false);
                }
                if e.code() != ERROR_PIPE_CONNECTED.to_hresult() {
                    return Err(e.into());
                }
            }
            let mut actual = 0;
            GetNamedPipeClientProcessId(self.handle.0, &mut actual)?;
            if actual != pid {
                bail!("pipe: unexpected client PID {actual}, expected {pid}");
            }
        }
        self.connected = true;
        Ok(true)
    }
    pub fn send<T: Serialize>(&self, message: &T) -> Result<()> {
        let bytes = serde_json::to_vec(message)?;
        if bytes.len() > 8192 {
            bail!("pipe: message exceeds 8 KiB");
        }
        let mut written = 0;
        unsafe {
            WriteFile(self.handle.0, Some(&bytes), Some(&mut written), None)?;
        }
        if written as usize != bytes.len() {
            bail!("pipe: message write would block");
        }
        Ok(())
    }
    pub fn receive<T: DeserializeOwned>(&self) -> Result<Option<T>> {
        let mut available = 0;
        unsafe {
            PeekNamedPipe(self.handle.0, None, 0, None, Some(&mut available), None)?;
        }
        if available == 0 {
            return Ok(None);
        }
        let mut bytes = [0u8; 8192];
        let mut read = 0;
        unsafe {
            ReadFile(self.handle.0, Some(&mut bytes), Some(&mut read), None)?;
        }
        if read == 0 {
            bail!("pipe: EOF");
        }
        Ok(Some(serde_json::from_slice(&bytes[..read as usize])?))
    }
}
