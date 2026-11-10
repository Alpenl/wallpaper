//! One writer for the interactive app. The foreground owns admission and cached state;
//! the worker owns all disk operations, including the full lifetime of an import job.
use crate::{
    import, library_ops, startup,
    storage::{Paths, Store},
};
use anyhow::{Context, Result, ensure};
use std::sync::mpsc::{self, Receiver, Sender};
use wallpaper_core::{
    config::Settings,
    displays::{Assignments, DisplayInfo},
    library::Library,
};
use wallpaper_ui::{DeleteConfirmation, Setting, UiCommand, UiItem};
pub struct Cache {
    pub library: Library,
    pub assignments: Assignments,
    pub settings: Settings,
    pub items: Vec<UiItem>,
}
pub enum Notification {
    Progress(String),
    Complete {
        wrote: bool,
        result: Result<Option<DeleteConfirmation>>,
        cache: Result<Cache>,
    },
}
pub fn completion_notice(wrote: bool, succeeded: bool) -> &'static str {
    if wrote && succeeded {
        "保存完成；显示结果请见主显示器状态及错误提示。"
    } else {
        ""
    }
}
fn is_write(command: &UiCommand) -> bool {
    matches!(
        command,
        UiCommand::Import(_)
            | UiCommand::AssignAll(_)
            | UiCommand::AssignDisplay(..)
            | UiCommand::Fit(..)
            | UiCommand::Delete(_)
            | UiCommand::Setting(_)
    )
}
struct Request {
    command: UiCommand,
    displays: Vec<DisplayInfo>,
    playing: Vec<String>,
}
pub struct Controller {
    sender: Sender<Request>,
    pub notifications: Receiver<Notification>,
    busy: bool,
}
impl Controller {
    pub fn new(paths: Paths) -> Self {
        let (sender, requests) = mpsc::channel::<Request>();
        let (send, notifications) = mpsc::channel();
        std::thread::spawn(move || {
            if send
                .send(Notification::Complete {
                    wrote: false,
                    result: Ok(None),
                    cache: load(&paths),
                })
                .is_err()
            {
                return;
            }
            for request in requests {
                let wrote = is_write(&request.command);
                let result = execute(&paths, request, &send);
                let cache = load(&paths);
                if send
                    .send(Notification::Complete {
                        wrote,
                        result,
                        cache,
                    })
                    .is_err()
                {
                    break;
                }
            }
        });
        Self {
            sender,
            notifications,
            busy: true,
        }
    }
    pub fn submit(
        &mut self,
        command: UiCommand,
        displays: Vec<DisplayInfo>,
        playing: Vec<String>,
        play: bool,
    ) -> Result<()> {
        ensure!(!self.busy, "正在处理操作，请等待完成后重试");
        ensure!(
            !(play
                && matches!(
                    command,
                    UiCommand::AssignAll(_) | UiCommand::AssignDisplay(..) | UiCommand::Fit(..)
                )),
            "--play 模式不支持持久分配操作"
        );
        self.sender.send(Request {
            command,
            displays,
            playing,
        })?;
        self.busy = true;
        Ok(())
    }
    pub fn completed(&mut self) {
        self.busy = false;
    }
    pub fn busy(&self) -> bool {
        self.busy
    }
}
pub fn load(paths: &Paths) -> Result<Cache> {
    let report = Store::inspect(paths);
    ensure!(!report.has_errors(), "存储读取失败，请运行 --store-check");
    Ok(Cache {
        library: report.library.value.context("missing library")?,
        assignments: report.assignments.value.context("missing assignments")?,
        settings: report.config.value.context("missing config")?,
        items: library_ops::list(paths, "")?
            .into_iter()
            .map(|i| UiItem {
                item: i.item,
                thumbnail: i.thumbnail,
            })
            .collect(),
    })
}
#[cfg(test)]
fn preview(paths: &Paths, id: &str, playing: &[String]) -> Result<DeleteConfirmation> {
    preview_on_displays(paths, id, playing, &[])
}
fn preview_on_displays(
    paths: &Paths,
    id: &str,
    playing: &[String],
    displays: &[DisplayInfo],
) -> Result<DeleteConfirmation> {
    let p = library_ops::delete_preview(paths, id)?;
    let entry = std::path::Path::new(&p.item.entry);
    let media = if entry.is_absolute() {
        entry.to_path_buf()
    } else {
        paths.library.join(id).join(entry)
    };
    let playing_path = playing
        .iter()
        .filter_map(|v| v.strip_prefix("path:"))
        .any(|v| {
            let path = std::path::Path::new(v);
            path == media
                || path
                    .canonicalize()
                    .ok()
                    .zip(media.canonicalize().ok())
                    .is_some_and(|(a, b)| a == b)
        });
    let blocked = playing_path
        || p.affects_default
        || !p.displays.is_empty()
        || playing.iter().any(|v| v == id);
    let mut description = format!("删除“{}”？此操作会移除素材及其缩略图。", p.item.title);
    if p.affects_default {
        description.push_str("\n正在作为默认壁纸。");
    }
    for display in &p.displays {
        let name = displays
            .iter()
            .find(|d| &d.id == display)
            .map(|d| d.name.as_str())
            .unwrap_or(&display.0);
        description.push_str(&format!("\n正在显示器 {name} 上使用。"));
    }
    if playing_path || playing.iter().any(|v| v == id) {
        description.push_str("\n此素材正在播放。");
    }
    Ok(DeleteConfirmation {
        id: id.into(),
        blocked,
        description,
    })
}
pub fn update_setting(paths: &Paths, setting: Setting) -> Result<()> {
    if let Setting::Launch(enabled) = setting {
        let code = startup::set(
            paths,
            &wallpaper_platform::startup::Backend::from_env()?,
            &std::env::current_exe()?,
            enabled,
        )?;
        ensure!(code == 0, "开机启动设置失败（{code}）");
        return Ok(());
    }
    let report = Store::inspect(paths);
    ensure!(!report.has_errors(), "存储配置无效");
    let mut config = report.config.value.context("missing config")?;
    match setting {
        Setting::Battery(v) => config.pause.battery_policy = v,
        Setting::Threshold(v) => config.pause.low_battery_pct = v,
        Setting::Fullscreen(v) => config.pause.on_fullscreen = v,
        Setting::Covered(v) => config.pause.on_covered = v,
        Setting::OpenWindow(v) => config.startup.open_settings = v,
        Setting::Launch(_) => unreachable!(),
    }
    config.normalize();
    Store::save_config(paths, &config)
}
fn finish_import(job: import::Job, send: &Sender<Notification>) -> Result<()> {
    let mut rejections = Vec::new();
    while let Ok(event) = job.events.recv() {
        let text = match event {
            import::Event::Started { index, total, .. } => format!("导入 {}/{}", index + 1, total),
            import::Event::Copying { done, total, .. } => format!("复制 {done}/{total} 字节"),
            import::Event::Probing { index } => format!("检查第 {} 项媒体", index + 1),
            import::Event::Rejected { detail, .. } => {
                rejections.push(detail.clone());
                format!("素材被拒绝：{detail}")
            }
            import::Event::Finished { .. } => break,
            _ => continue,
        };
        let _ = send.send(Notification::Progress(text));
    }
    let summary = job.join()?;
    ensure!(
        summary.rejected == 0 && !summary.cancelled,
        "导入完成：{} 成功，{} 拒绝，取消：{}。{}",
        summary.imported,
        summary.rejected,
        summary.cancelled,
        rejections.join("；")
    );
    Ok(())
}
fn execute(
    paths: &Paths,
    r: Request,
    send: &Sender<Notification>,
) -> Result<Option<DeleteConfirmation>> {
    match r.command {
        UiCommand::Import(sources) => finish_import(
            import::start(paths.clone(), std::env::current_exe()?, sources),
            send,
        )?,
        UiCommand::AssignAll(id) => library_ops::assign_all(paths, &id)?,
        UiCommand::AssignDisplay(id, display) => {
            library_ops::assign_display(paths, &id, &display, &r.displays)?
        }
        UiCommand::Fit(id, fit) => library_ops::set_fit(paths, &id, fit)?,
        UiCommand::Setting(setting) => update_setting(paths, setting)?,
        UiCommand::PreviewDelete(id) => {
            return Ok(Some(preview_on_displays(
                paths,
                &id,
                &r.playing,
                &r.displays,
            )?));
        }
        UiCommand::Delete(id) => {
            ensure!(
                !preview_on_displays(paths, &id, &r.playing, &r.displays)?.blocked,
                "条目正在被分配或播放，请先切换壁纸"
            );
            library_ops::delete(paths, &id)?;
        }
        _ => anyhow::bail!("unsupported storage command"),
    }
    Ok(None)
}
#[cfg(test)]
mod tests {
    use super::*;
    fn paths() -> Paths {
        let root = crate::test_temp("controller");
        Paths {
            config: root.join("config"),
            library: root.join("library"),
            cache: root.join("cache"),
            logs: root.join("logs"),
        }
    }
    #[test]
    fn notices_only_follow_successful_writes() {
        assert_eq!(completion_notice(false, true), ""); // startup read
        assert!(!is_write(&UiCommand::PreviewDelete("one".into())));
        assert_eq!(
            completion_notice(is_write(&UiCommand::PreviewDelete("one".into())), true),
            ""
        );
        assert!(!completion_notice(is_write(&UiCommand::AssignAll("one".into())), true).is_empty());
        assert_eq!(completion_notice(true, false), "");
    }
    #[test]
    fn busy_rejects_before_enqueue_and_releases_on_completion() {
        let p = paths();
        let mut c = Controller::new(p.clone());
        assert!(c.busy());
        assert!(matches!(
            c.notifications.recv().unwrap(),
            Notification::Complete { .. }
        ));
        c.completed();
        c.submit(
            UiCommand::Setting(Setting::Threshold(25)),
            vec![],
            vec![],
            false,
        )
        .unwrap();
        assert!(
            c.submit(UiCommand::Import(vec![]), vec![], vec![], false)
                .is_err()
        );
        assert!(matches!(
            c.notifications.recv().unwrap(),
            Notification::Complete { .. }
        ));
        assert!(c.busy());
        c.completed();
        assert!(!c.busy());
        assert!(
            c.submit(UiCommand::AssignAll("one".into()), vec![], vec![], true)
                .is_err()
        );
        std::fs::remove_dir_all(p.config.parent().unwrap()).unwrap();
    }
    #[test]
    fn final_import_error_survives_finished_event() {
        let job = import::spawn(|_, _| anyhow::bail!("final storage failure"));
        let (send, _) = mpsc::channel();
        assert!(
            finish_import(job, &send)
                .unwrap_err()
                .to_string()
                .contains("final storage failure")
        );
    }
    #[test]
    fn setting_reads_latest_and_normalizes() {
        let p = paths();
        let mut config = Settings::default();
        config.startup.open_settings = false;
        config.pause.on_covered = false;
        Store::save_config(&p, &config).unwrap();
        update_setting(&p, Setting::Threshold(37)).unwrap();
        config.pause.low_battery_pct = 37;
        assert_eq!(Store::inspect(&p).config.value.unwrap(), config);
        update_setting(&p, Setting::Threshold(255)).unwrap();
        assert_eq!(
            Store::inspect(&p)
                .config
                .value
                .unwrap()
                .pause
                .low_battery_pct,
            20
        );
        std::fs::remove_dir_all(p.config.parent().unwrap()).unwrap();
    }
    #[test]
    fn delete_rechecks_assignment_and_playing() {
        let p = paths();
        let item = wallpaper_core::library::Item {
            id: "one".into(),
            title: "one".into(),
            kind: wallpaper_core::library::Kind::Image,
            entry: "media.png".into(),
            width: None,
            height: None,
            duration_sec: None,
            size_bytes: None,
            added_at: String::new(),
            fit: Default::default(),
        };
        Store::save_library(
            &p,
            &Library {
                items: vec![item],
                ..Default::default()
            },
        )
        .unwrap();
        assert!(!preview(&p, "one", &[]).unwrap().blocked);
        assert!(preview(&p, "one", &["one".into()]).unwrap().blocked);
        Store::save_assignments(
            &p,
            &Assignments {
                default: Some("one".into()),
                ..Default::default()
            },
        )
        .unwrap();
        let (send, _) = mpsc::channel();
        assert!(
            execute(
                &p,
                Request {
                    command: UiCommand::Delete("one".into()),
                    displays: vec![],
                    playing: vec![]
                },
                &send
            )
            .is_err()
        );
        assert!(
            Store::inspect(&p)
                .library
                .value
                .unwrap()
                .find("one")
                .is_some()
        );
        std::fs::remove_dir_all(p.config.parent().unwrap()).unwrap();
    }
}
