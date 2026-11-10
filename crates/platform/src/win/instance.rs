use std::path::Path;
use windows::Win32::Networking::WinSock::{
    ADDRESS_FAMILY, AF_UNIX, SOCK_STREAM, SOCKADDR, SOCKADDR_UN, WSACleanup, WSADATA, WSAStartup,
    closesocket, connect as socket_connect, socket,
};

pub(super) fn connect(path: &Path) -> bool {
    let Some(path) = path.to_str() else {
        return false;
    };
    let mut address = SOCKADDR_UN {
        sun_family: ADDRESS_FAMILY(AF_UNIX),
        ..Default::default()
    };
    if path.len() >= address.sun_path.len() {
        return false;
    }
    for (slot, byte) in address.sun_path.iter_mut().zip(path.bytes()) {
        *slot = byte as i8;
    }
    unsafe {
        let mut data = WSADATA::default();
        if WSAStartup(0x0202, &mut data) != 0 {
            return false;
        }
        let connected = if let Ok(socket) = socket(i32::from(AF_UNIX), SOCK_STREAM, 0) {
            let connected = socket_connect(
                socket,
                &address as *const SOCKADDR_UN as *const SOCKADDR,
                size_of::<SOCKADDR_UN>() as i32,
            ) == 0;
            closesocket(socket);
            connected
        } else {
            false
        };
        WSACleanup();
        connected
    }
}
