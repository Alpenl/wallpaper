use anyhow::Result;
use tray_icon::{
    Icon, TrayIcon, TrayIconBuilder,
    menu::{Menu, MenuItem},
};

pub(crate) struct Tray {
    _icon: TrayIcon,
    pub(crate) open: MenuItem,
    pub(crate) pause: MenuItem,
    pub(crate) quit: MenuItem,
}
impl Tray {
    pub(crate) fn new() -> Result<Self> {
        let menu = Menu::new();
        let open = MenuItem::new("打开设置", true, None);
        let pause = MenuItem::new("暂停", true, None);
        let quit = MenuItem::new("退出", true, None);
        menu.append_items(&[&open, &pause, &quit])?;
        let icon = Icon::from_rgba([50, 150, 220, 255].repeat(16 * 16), 16, 16)?;
        let icon = TrayIconBuilder::new()
            .with_menu(Box::new(menu))
            .with_icon(icon)
            .with_tooltip("Wallpaper spike")
            .build()?;
        Ok(Self {
            _icon: icon,
            open,
            pause,
            quit,
        })
    }
}
