use super::MainThread;
use wallpaper_core::policy::Power;
use windows::Win32::System::Power::{GetSystemPowerStatus, SYSTEM_POWER_STATUS};
pub fn power(_main: MainThread) -> Option<Power> {
    let mut status = SYSTEM_POWER_STATUS::default();
    let ok = unsafe { GetSystemPowerStatus(&mut status) }.is_ok();
    let result = if !ok || status.ACLineStatus == 255 {
        None
    } else if status.BatteryFlag & 128 != 0 {
        Some(Power {
            on_battery: false,
            percent: None,
        })
    } else {
        match status.ACLineStatus {
            0 | 1 => Some(Power {
                on_battery: status.ACLineStatus == 0,
                percent: (status.BatteryLifePercent <= 100).then_some(status.BatteryLifePercent),
            }),
            _ => None,
        }
    };
    crate::signal_health("power", result.is_some());
    result
}
