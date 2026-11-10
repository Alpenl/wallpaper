use super::MainThread;
use objc2_core_foundation::{
    CFArray, CFBoolean, CFDictionary, CFNumber, CFRetained, CFString, CFType,
};
use std::ptr::NonNull;
use wallpaper_core::policy::Power;

#[link(name = "IOKit", kind = "framework")]
unsafe extern "C" {
    fn IOPSCopyPowerSourcesInfo() -> *mut CFType;
    fn IOPSGetProvidingPowerSourceType(info: &CFType) -> *const CFString;
    fn IOPSCopyPowerSourcesList(info: &CFType) -> *mut CFArray<CFType>;
    fn IOPSGetPowerSourceDescription(
        info: &CFType,
        source: &CFType,
    ) -> *const CFDictionary<CFString, CFType>;
}
fn query() -> Option<Power> {
    // Copy/Create references are owned; Get references remain borrowed from info.
    let info = unsafe { CFRetained::from_raw(NonNull::new(IOPSCopyPowerSourcesInfo())?) };
    let kind = unsafe { IOPSGetProvidingPowerSourceType(&info).as_ref()? };
    let list = unsafe { CFRetained::from_raw(NonNull::new(IOPSCopyPowerSourcesList(&info))?) };
    let mut percent = None;
    for source in list.iter() {
        let Some(description) = (unsafe { IOPSGetPowerSourceDescription(&info, &source).as_ref() })
        else {
            continue;
        };
        let get = |key: &str| description.get(CFString::from_str(key).as_ref());
        if get("Type")
            .and_then(|v| v.downcast_ref::<CFString>().map(|s| s.to_string()))
            .as_deref()
            != Some("InternalBattery")
            || !get("Is Present")
                .and_then(|v| v.downcast_ref::<CFBoolean>().map(CFBoolean::as_bool))
                .unwrap_or(false)
        {
            continue;
        }
        let number =
            |key| get(key).and_then(|v| v.downcast_ref::<CFNumber>().and_then(CFNumber::as_f64));
        if let (Some(current), Some(max)) = (number("Current Capacity"), number("Max Capacity"))
            && current.is_finite()
            && max.is_finite()
            && current >= 0.0
            && max > 0.0
        {
            percent = Some((current / max * 100.0).round().clamp(0.0, 100.0) as u8);
            break;
        }
    }
    Some(Power {
        on_battery: kind.to_string() == "Battery Power",
        percent,
    })
}
pub fn power(_main: MainThread) -> Option<Power> {
    let result = query();
    crate::signal_health("power", result.is_some());
    result
}
