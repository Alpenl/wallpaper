import { useEffect, useState, type ReactNode } from "react"

type Lang = "zh" | "en"
type Theme = "light" | "dark"
type Page = "library" | "displays" | "playback" | "rules" | "general" | "versions" | "about"

const dict = {
  zh: {
    app: "动态壁纸",
    library: "壁纸库",
    displays: "显示器",
    playback: "播放与性能",
    rules: "暂停与电源",
    general: "通用",
    versions: "版本管理",
    about: "关于与诊断",
    groupWall: "壁纸",
    groupSet: "设置",
    groupSys: "系统",
    playing: "正在播放",
    pausedAll: "已暂停",
    screens: "2 块屏幕",
    pauseAll: "暂停全部",
    resume: "继续播放",
    next: "下一张",
    libSub: "6 个壁纸 · 拖入文件即可导入",
    search: "搜索壁纸",
    import: "导入",
    all: "全部",
    video: "视频",
    image: "图片",
    inUse: "使用中",
    applyTo: "应用到",
    apply: "应用",
    allDisplays: "所有显示器",
    primary: "主显示器",
    secondary: "副显示器",
    source: "来源",
    resolution: "分辨率",
    added: "导入时间",
    localFile: "本地文件",
    today: "今天",
    dispSub: "每块屏幕可独立设置壁纸，暂停按屏判定",
    wallpaper: "壁纸",
    fit: "填充方式",
    fill: "填充",
    fitMode: "适应",
    stretch: "拉伸",
    status: "状态",
    live: "播放中",
    hw: "硬件解码",
    playSub: "可见时硬解零拷贝，不可见时自动暂停",
    profile: "性能档位",
    smooth: "流畅",
    smoothD: "原始帧率",
    balanced: "均衡",
    balancedD: "推荐，兼顾流畅与功耗",
    saver: "省电",
    saverD: "限制为 15 fps",
    backend: "渲染后端",
    hwD: "遇到花屏或黑屏时可关闭",
    mute: "静音播放",
    muteD: "去除音轨，循环更顺滑",
    rulesSub: "看不见的壁纸不消耗资源",
    autoPause: "自动暂停",
    fullscreen: "其他程序全屏时",
    fullscreenD: "游戏或视频全屏时暂停所在屏幕",
    covered: "桌面被完全遮挡时",
    coveredD: "窗口覆盖 95% 以上桌面",
    lock: "锁屏与睡眠时",
    always: "始终启用",
    battery: "使用电池时",
    bPause: "暂停",
    bFps: "降至 15 fps",
    bKeep: "正常播放",
    lowBat: "低电量阈值",
    lowBatD: "低于此电量时停止渲染",
    genSub: "外观、语言与启动行为",
    appearance: "外观",
    theme: "主题",
    light: "浅色",
    dark: "深色",
    language: "语言",
    accent: "强调色",
    startup: "启动",
    launch: "开机自动启动",
    launchD: "登录系统后在后台运行",
    tray: "关闭窗口时最小化到托盘",
    trayD: "壁纸继续在后台播放",
    verSub: "更新通道、版本历史与回滚",
    current: "当前版本",
    latest: "已是最新版本",
    check: "检查更新",
    checking: "正在检查…",
    channel: "更新通道",
    stable: "稳定版",
    beta: "测试版",
    nightly: "每日构建",
    autoUpdate: "自动检查更新",
    autoUpdateD: "启动时检查，仅下载清单",
    autoInstall: "自动安装更新",
    autoInstallD: "下次启动时生效",
    history: "版本历史",
    installed: "当前",
    rollback: "回滚",
    delta: "增量更新与签名校验：失败时自动回滚到上一版本",
    keep: "保留旧版本",
    keepD: "用于回滚的本地版本数",
    aboutSub: "版本信息、诊断与开源许可",
    aboutD: "性能优先的跨平台动态壁纸，原生渲染，不使用 WebView。",
    diag: "诊断",
    export: "导出诊断包",
    logs: "打开日志",
    privacy: "不收集任何遥测数据，日志仅保存在本机。",
    licenses: "开源许可",
    openSettings: "打开设置",
    openLibrary: "打开壁纸库",
    quit: "退出",
    trayTitle: "托盘",
    recent: "最近使用",
    toastApplied: "已应用",
    toastUpToDate: "已是最新版本",
    toastRolled: "已回滚，重启后生效",
    toastExported: "诊断包已导出",
    speed: "播放速度",
    speedD: "修改后即时生效，不重新加载",
    perScreenProfile: "性能档位",
    perScreenProfileD: "覆盖全局设置",
    followGlobal: "跟随全局",
    pauseScreen: "暂停此屏幕",
    pauseScreenD: "仅停止当前屏幕的渲染",
    resStatus: "资源状态",
    level: "当前级别",
    levelV: "L0 · 全速",
    decode: "解码",
    hwOk: "硬解",
    mode: "显示模式",
    saved: "本周省电",
    multi: "多显示器",
    layout: "布局方式",
    layoutD: "跨屏拼接时无法按屏暂停",
    independent: "独立",
    duplicate: "复制",
    span: "跨屏",
    remember: "记住每块屏幕的壁纸",
    rememberD: "重新连接或调整分辨率后自动恢复",
    custom: "自定义",
    fpsCap: "帧率上限",
    fpsSrc: "原始",
    scale: "渲染缩放",
    scaleD: "降低分辨率以减少 GPU 负载",
    decoding: "解码与输出",
    resumePos: "从上次位置继续播放",
    resumePosD: "重启后各屏幕从中断处续播",
    importGrp: "导入与转码",
    transcode: "导入时自动转码",
    transcodeD: "统一时长、插入关键帧、去除音轨，消除循环点卡顿",
    targetRes: "目标分辨率",
    matchDisplay: "匹配显示器",
    targetFps: "目标帧率",
    encoder: "编码器",
    sysEncoder: "系统硬件编码器",
    swEncoder: "软件编码 (x264)",
    gif: "GIF 转为视频",
    gifD: "按视频播放，显著降低 CPU 占用",
    cache: "转码缓存",
    cacheD: "可随时清除，需要时自动重建",
    clear: "清除",
    maximized: "其他窗口最大化时",
    maximizedD: "最大化窗口所在的屏幕暂停",
    remote: "远程桌面会话中",
    remoteD: "通过远程连接登录时停止渲染",
    monitorOff: "显示器关闭时",
    behavior: "暂停行为",
    scope: "暂停范围",
    scopeD: "条件触发时影响的屏幕",
    scopeOne: "所在屏幕",
    scopeAll: "所有屏幕",
    pauseMode: "暂停方式",
    pauseModeD: "停止可释放显存，但恢复更慢",
    pmPause: "暂停",
    pmStop: "停止",
    debounce: "恢复延迟",
    debounceD: "避免窗口频繁切换时反复启停",
    appRules: "应用规则",
    appRulesD: "仅按可执行文件匹配，不读取窗口标题",
    addApp: "添加程序",
    acPause: "暂停",
    acSaver: "省电档",
    acIgnore: "忽略全屏",
    lowPower: "跟随系统省电模式",
    lowPowerD: "Windows 节电模式或 macOS 低电量模式时暂停",
    thermal: "过热时降级",
    thermalD: "系统温度过高时降至 15 fps",
    restoreLast: "启动时恢复上次壁纸",
    restoreLastD: "包括每块屏幕的分配与播放进度",
    shortcuts: "全局快捷键",
    scPause: "暂停 / 继续全部",
    scNext: "切换到下一张",
    scOpen: "打开设置",
    storage: "存储位置",
    libPath: "壁纸库",
    cachePath: "缓存",
    change: "更改",
    metrics: "本机统计",
    metricsD: "近 7 天数据，仅保存在本机",
    pauseLat: "暂停延迟 < 300 ms",
    hwRate: "硬解成功率",
    crashes: "崩溃重启",
    exportD: "导出前可预览，不包含素材路径与窗口标题",
    debugMode: "诊断模式",
    debugModeD: "日志级别提升为 debug，30 分钟后自动关闭",
    privacyGrp: "隐私",
    telemetry: "发送匿名使用统计",
    telemetryD: "仅聚合数据，默认关闭；关闭后不产生任何网络请求",
    crash: "崩溃报告",
    crashAsk: "每次询问",
    crashAuto: "自动发送",
    crashNever: "从不",
    installId: "安装 ID",
    installIdD: "随机生成，每 90 天轮换",
    reset: "重置",
    toastCleared: "缓存已清除",
    toastReset: "安装 ID 已重置",
  },
  en: {
    app: "Wallpaper",
    library: "Library",
    displays: "Displays",
    playback: "Playback",
    rules: "Pause & Power",
    general: "General",
    versions: "Versions",
    about: "About",
    groupWall: "Wallpapers",
    groupSet: "Settings",
    groupSys: "System",
    playing: "Playing",
    pausedAll: "Paused",
    screens: "2 displays",
    pauseAll: "Pause all",
    resume: "Resume",
    next: "Next",
    libSub: "6 wallpapers · Drop files to import",
    search: "Search",
    import: "Import",
    all: "All",
    video: "Video",
    image: "Image",
    inUse: "In use",
    applyTo: "Apply to",
    apply: "Apply",
    allDisplays: "All displays",
    primary: "Primary",
    secondary: "Secondary",
    source: "Source",
    resolution: "Resolution",
    added: "Added",
    localFile: "Local file",
    today: "Today",
    dispSub: "Set a wallpaper per display. Pausing is evaluated per screen.",
    wallpaper: "Wallpaper",
    fit: "Fit",
    fill: "Fill",
    fitMode: "Fit",
    stretch: "Stretch",
    status: "Status",
    live: "Playing",
    hw: "Hardware decoding",
    playSub: "Zero-copy decoding when visible, paused when hidden",
    profile: "Performance",
    smooth: "Smooth",
    smoothD: "Source frame rate",
    balanced: "Balanced",
    balancedD: "Recommended balance",
    saver: "Saver",
    saverD: "Capped at 15 fps",
    backend: "Renderer",
    hwD: "Turn off if you see artifacts",
    mute: "Muted playback",
    muteD: "Strip audio for seamless loops",
    rulesSub: "Wallpapers you cannot see use no resources",
    autoPause: "Auto pause",
    fullscreen: "When an app is fullscreen",
    fullscreenD: "Pause the screen running a game or video",
    covered: "When desktop is covered",
    coveredD: "Windows cover over 95% of the desktop",
    lock: "When locked or asleep",
    always: "Always on",
    battery: "On battery",
    bPause: "Pause",
    bFps: "15 fps",
    bKeep: "Keep playing",
    lowBat: "Low battery",
    lowBatD: "Stop rendering below this level",
    genSub: "Appearance, language and startup",
    appearance: "Appearance",
    theme: "Theme",
    light: "Light",
    dark: "Dark",
    language: "Language",
    accent: "Accent",
    startup: "Startup",
    launch: "Launch at login",
    launchD: "Run in the background after sign-in",
    tray: "Minimize to tray on close",
    trayD: "Wallpapers keep playing",
    verSub: "Update channel, history and rollback",
    current: "Current version",
    latest: "You are up to date",
    check: "Check for updates",
    checking: "Checking…",
    channel: "Channel",
    stable: "Stable",
    beta: "Beta",
    nightly: "Nightly",
    autoUpdate: "Check automatically",
    autoUpdateD: "On launch, manifest only",
    autoInstall: "Install automatically",
    autoInstallD: "Applied on next launch",
    history: "History",
    installed: "Current",
    rollback: "Roll back",
    delta:
      "Delta updates with signature checks. Failed updates roll back automatically.",
    keep: "Keep old versions",
    keepD: "Local builds kept for rollback",
    aboutSub: "Version, diagnostics and licenses",
    aboutD:
      "A performance-first cross-platform live wallpaper. Native rendering, no WebView.",
    diag: "Diagnostics",
    export: "Export report",
    logs: "Open logs",
    privacy: "No telemetry. Logs stay on this device.",
    licenses: "Licenses",
    openSettings: "Settings",
    openLibrary: "Library",
    quit: "Quit",
    trayTitle: "Tray",
    recent: "Recent",
    toastApplied: "Applied",
    toastUpToDate: "You are up to date",
    toastRolled: "Rolled back. Restart to apply.",
    toastExported: "Report exported",
    speed: "Playback speed",
    speedD: "Applies instantly without reloading",
    perScreenProfile: "Performance",
    perScreenProfileD: "Overrides the global setting",
    followGlobal: "Follow global",
    pauseScreen: "Pause this display",
    pauseScreenD: "Stops rendering on this screen only",
    resStatus: "Resources",
    level: "Level",
    levelV: "L0 · Full",
    decode: "Decoder",
    hwOk: "HW",
    mode: "Mode",
    saved: "Saved this week",
    multi: "Multiple displays",
    layout: "Layout",
    layoutD: "Per-screen pausing is unavailable in span mode",
    independent: "Independent",
    duplicate: "Duplicate",
    span: "Span",
    remember: "Remember per-display wallpapers",
    rememberD: "Restored after reconnecting or changing resolution",
    custom: "Custom",
    fpsCap: "Frame rate cap",
    fpsSrc: "Source",
    scale: "Render scale",
    scaleD: "Lower resolution to reduce GPU load",
    decoding: "Decoding & output",
    resumePos: "Resume from last position",
    resumePosD: "Each display continues where it stopped",
    importGrp: "Import & transcoding",
    transcode: "Transcode on import",
    transcodeD: "Even duration, keyframes, no audio for seamless loops",
    targetRes: "Target resolution",
    matchDisplay: "Match display",
    targetFps: "Target frame rate",
    encoder: "Encoder",
    sysEncoder: "System hardware",
    swEncoder: "Software (x264)",
    gif: "Convert GIF to video",
    gifD: "Plays as video with far lower CPU use",
    cache: "Transcode cache",
    cacheD: "Safe to clear, rebuilt when needed",
    clear: "Clear",
    maximized: "When a window is maximized",
    maximizedD: "Pause the screen with the maximized window",
    remote: "During remote desktop",
    remoteD: "Stop rendering in remote sessions",
    monitorOff: "When display is off",
    behavior: "Pause behavior",
    scope: "Scope",
    scopeD: "Screens affected when a rule triggers",
    scopeOne: "That display",
    scopeAll: "All displays",
    pauseMode: "Pause mode",
    pauseModeD: "Stop frees video memory but resumes slower",
    pmPause: "Pause",
    pmStop: "Stop",
    debounce: "Resume delay",
    debounceD: "Avoids flicker when switching windows quickly",
    appRules: "App rules",
    appRulesD: "Matched by executable only, window titles are never read",
    addApp: "Add app",
    acPause: "Pause",
    acSaver: "Saver",
    acIgnore: "Ignore fullscreen",
    lowPower: "Follow system power saving",
    lowPowerD: "Pause in Windows Energy Saver or macOS Low Power Mode",
    thermal: "Throttle when hot",
    thermalD: "Drop to 15 fps under thermal pressure",
    restoreLast: "Restore last session",
    restoreLastD: "Per-display wallpapers and playback position",
    shortcuts: "Global shortcuts",
    scPause: "Pause / resume all",
    scNext: "Next wallpaper",
    scOpen: "Open settings",
    storage: "Storage",
    libPath: "Library",
    cachePath: "Cache",
    change: "Change",
    metrics: "Local metrics",
    metricsD: "Last 7 days, stored on this device only",
    pauseLat: "Pause under 300 ms",
    hwRate: "HW decode rate",
    crashes: "Restarts",
    exportD: "Preview before export. No media paths or window titles.",
    debugMode: "Diagnostic mode",
    debugModeD: "Debug logging, turns off after 30 minutes",
    privacyGrp: "Privacy",
    telemetry: "Share anonymous usage stats",
    telemetryD: "Aggregates only, off by default. No network requests when off.",
    crash: "Crash reports",
    crashAsk: "Ask",
    crashAuto: "Send",
    crashNever: "Never",
    installId: "Install ID",
    installIdD: "Random, rotated every 90 days",
    reset: "Reset",
    toastCleared: "Cache cleared",
    toastReset: "Install ID reset",
  },
}
type T = typeof dict.zh

const img = (id: string) =>
  `https://images.unsplash.com/${id}?auto=format&fit=crop&w=1200&q=80`
const walls = [
  {
    id: 1,
    zh: "雾林",
    en: "Misty Pines",
    type: "video",
    src: img("photo-1509101758186-479a713d3687"),
    spec: "4K · 30fps",
  },
  {
    id: 2,
    zh: "湖光",
    en: "Still Lake",
    type: "video",
    src: img("photo-1641147036074-12b1c3500235"),
    spec: "4K · 24fps",
  },
  {
    id: 3,
    zh: "流光",
    en: "Golden Haze",
    type: "video",
    src: img("photo-1786702623105-674b7c839087"),
    spec: "1440p · 30fps",
  },
  {
    id: 4,
    zh: "云野",
    en: "Cloud Field",
    type: "image",
    src: img("photo-1717421823941-bd73c42b672b"),
    spec: "5K · JPG",
  },
  {
    id: 5,
    zh: "孤树",
    en: "Lone Tree",
    type: "image",
    src: img("photo-1787906410352-26562eceb0eb"),
    spec: "4K · WEBP",
  },
  {
    id: 6,
    zh: "赤原",
    en: "Red Grass",
    type: "video",
    src: img("photo-1697535493951-fc8c4ecd5c8f"),
    spec: "4K · 60fps",
  },
]
type W = typeof walls[number]
const versions = [
  {
    v: "0.2.1",
    date: "2025-06-12",
    zh: "显示器重命名、导入后行为可配置、修复多屏排序",
    en: "Display renaming, configurable import behavior, multi-monitor ordering fix",
  },
  {
    v: "0.2.0",
    date: "2025-05-28",
    zh: "一键转码、电池 15 fps 策略、安全模式",
    en: "One-click transcode, 15 fps battery policy, safe mode",
  },
  {
    v: "0.1.4",
    date: "2025-05-02",
    zh: "暂停规则状态机、远程桌面暂停",
    en: "Pause state machine, remote desktop pausing",
  },
  {
    v: "0.1.0",
    date: "2025-04-10",
    zh: "首个 MVP 版本",
    en: "First MVP release",
  },
]
const accents = ["#3b6ef6", "#7c5cf0", "#0f9d74", "#e0702a", "#d4457f"]

const P: Record<string, ReactNode> = {
  library: (
    <>
      <rect x="3.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="3.5" width="7" height="7" rx="1.5" />
      <rect x="3.5" y="13.5" width="7" height="7" rx="1.5" />
      <rect x="13.5" y="13.5" width="7" height="7" rx="1.5" />
    </>
  ),
  displays: (
    <>
      <rect x="2.5" y="4" width="19" height="12.5" rx="2" />
      <path d="M8.5 20h7M12 16.5V20" />
    </>
  ),
  playback: (
    <>
      <path d="M12 14.5l3.5-3.5" />
      <path d="M3.8 18.5a9 9 0 1 1 16.4 0" />
    </>
  ),
  rules: <path d="M13 3 5 13.5h6L10 21l8-10.5h-6z" />,
  general: (
    <>
      <path d="M4 7h10M18 7h2M4 17h4M12 17h8" />
      <circle cx="16" cy="7" r="2" />
      <circle cx="10" cy="17" r="2" />
    </>
  ),
  versions: (
    <>
      <path d="M12 7v5l3 2" />
      <path d="M3.5 12a8.5 8.5 0 1 0 2.6-6.1M3.5 4v4h4" />
    </>
  ),
  about: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M12 11v5M12 8h.01" />
    </>
  ),
  search: (
    <>
      <circle cx="11" cy="11" r="6.5" />
      <path d="m20 20-4-4" />
    </>
  ),
  plus: <path d="M12 5v14M5 12h14" />,
  play: <path d="M8 5.5v13l10-6.5z" />,
  pause: <path d="M9 5v14M15 5v14" />,
  next: <path d="M6 5.5v13l9-6.5zM18 5v14" />,
  check: <path d="m5 12.5 4.5 4.5L19 7.5" />,
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4" />
    </>
  ),
  moon: <path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z" />,
  globe: (
    <>
      <circle cx="12" cy="12" r="9" />
      <path d="M3 12h18M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18" />
    </>
  ),
  power: (
    <>
      <path d="M12 3v8" />
      <path d="M6.4 7a8 8 0 1 0 11.2 0" />
    </>
  ),
  wifi: (
    <>
      <path d="M2.5 9a14 14 0 0 1 19 0M5.5 12.5a9.5 9.5 0 0 1 13 0M8.8 16a4.5 4.5 0 0 1 6.4 0" />
      <circle cx="12" cy="19.5" r=".5" />
    </>
  ),
  volume: (
    <>
      <path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z" />
      <path d="M16 9a4 4 0 0 1 0 6" />
    </>
  ),
  battery: (
    <>
      <rect x="2.5" y="7" width="17" height="10" rx="2.5" />
      <path d="M22 11v2" />
      <rect
        x="5"
        y="9.5"
        width="10"
        height="5"
        rx="1"
        fill="currentColor"
        stroke="none"
      />
    </>
  ),
  folder: (
    <path d="M3.5 7.5a2 2 0 0 1 2-2h3.8l2 2h7.2a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z" />
  ),
  download: <path d="M12 4v11M7 10.5l5 5 5-5M5 20h14" />,
  x: <path d="M7 7l10 10M17 7 7 17" />,
  logo: (
    <>
      <rect x="3" y="4.5" width="18" height="15" rx="3" />
      <path d="m3 15 5-4.5 4 3.5 3-2.5 6 5" />
      <circle cx="15.5" cy="9" r="1.5" />
    </>
  ),
}
function I({ n, s = 16 }: { n: string; s?: number }) {
  return (
    <svg
      width={s}
      height={s}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.6"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      className="ic"
    >
      {P[n]}
    </svg>
  )
}
function Toggle({
  on,
  set,
  disabled,
}: {
  on: boolean
  set?: (v: boolean) => void
  disabled?: boolean
}) {
  return (
    <button
      className={`tg${on ? " on" : ""}`}
      disabled={disabled}
      onClick={() => set?.(!on)}
      role="switch"
      aria-checked={on}
    >
      <i />
    </button>
  )
}
function Seg<V extends string>({
  value,
  set,
  opts,
}: {
  value: V
  set: (v: V) => void
  opts: [V, ReactNode][]
}) {
  return (
    <div className="seg">
      {opts.map(([v, l]) => (
        <button
          key={v}
          className={value === v ? "on" : ""}
          onClick={() => set(v)}
        >
          {l}
        </button>
      ))}
    </div>
  )
}
function Row({ t, d, children }: { t: string; d?: string; children: ReactNode }) {
  return (
    <div className="row">
      <div>
        <div className="rt">{t}</div>
        {d && <div className="rd">{d}</div>}
      </div>
      <div className="rc">{children}</div>
    </div>
  )
}
function Group({ title, children }: { title?: string; children: ReactNode }) {
  return (
    <section className="grp">
      {title && <h3>{title}</h3>}
      <div className="grp-b">{children}</div>
    </section>
  )
}
function Head({
  title,
  sub,
  right,
}: {
  title: string
  sub: string
  right?: ReactNode
}) {
  return (
    <header className="head">
      <div>
        <h1>{title}</h1>
        <p>{sub}</p>
      </div>
      {right}
    </header>
  )
}

type Ctx = {
  t: T
  name: (w: W) => string
  setToast: (s: string) => void
  assigned: [number, number]
  setAssigned: (a: [number, number]) => void
}

export default function App() {
  const [lang, setLang] = useState<Lang>("zh")
  const [theme, setTheme] = useState<Theme>("light")
  const [accent, setAccent] = useState(accents[0])
  const [page, setPage] = useState<Page>("library")
  const [paused, setPaused] = useState(false)
  const [tray, setTray] = useState(
    () => typeof window === "undefined" || window.innerWidth >= 1280,
  )
  const [assigned, setAssigned] = useState<[number, number]>([1, 2])
  const [toast, setToast] = useState("")
  const t = dict[lang]
  const name = (w: W) => w[lang]

  useEffect(() => {
    if (!toast) return
    const h = setTimeout(() => setToast(""), 2200)
    return () => clearTimeout(h)
  }, [toast])

  const nav: [string, Page[]][] = [
    [t.groupWall, ["library", "displays"]],
    [t.groupSet, ["playback", "rules", "general", "versions", "about"]],
  ]
  const ctx: Ctx = { t, name, setToast, assigned, setAssigned }
  const body: Record<Page, ReactNode> = {
    library: <Library {...ctx} />,
    displays: <Displays {...ctx} />,
    playback: <Playback t={t} setToast={setToast} />,
    rules: <Rules t={t} />,
    general: (
      <General
        t={t}
        lang={lang}
        setLang={setLang}
        theme={theme}
        setTheme={setTheme}
        accent={accent}
        setAccent={setAccent}
      />
    ),
    versions: <Versions t={t} lang={lang} setToast={setToast} />,
    about: <About t={t} setToast={setToast} />,
  }
  const cur = walls.find((w) => w.id === assigned[0])!
  const nextWall = () => setAssigned([(assigned[0] % 6) + 1, assigned[1]])

  return (
    <div className={`desk ${theme}${tray ? " tray-on" : ""}`} style={{ ["--ac" as string]: accent }}>
      <img className="desk-bg" src={cur.src} alt="" />
      <div className="win">
        <aside className="side">
          <div className="lights">
            <i />
            <i />
            <i />
          </div>
          <div className="brand">
            <span className="logo">
              <I n="logo" s={15} />
            </span>
            <span className="brand-t">{t.app}</span>
          </div>
          <nav>
            {nav.map(([g, ps]) => (
              <div key={g} className="ng">
                <div className="nl">{g}</div>
                {ps.map((p) => (
                  <button
                    key={p}
                    className={page === p ? "on" : ""}
                    onClick={() => setPage(p)}
                    title={t[p]}
                  >
                    <I n={p} />
                    <span>{t[p]}</span>
                  </button>
                ))}
              </div>
            ))}
          </nav>
          <div className="now">
            <img src={cur.src} alt="" />
            <div className="now-t">
              <span className={`dot${paused ? " off" : ""}`} />
              {paused ? t.pausedAll : t.playing}
            </div>
            <div className="now-n">
              {name(cur)} · {t.screens}
            </div>
            <div className="now-a">
              <button
                onClick={() => setPaused(!paused)}
                aria-label={t.pauseAll}
              >
                <I n={paused ? "play" : "pause"} s={14} />
              </button>
              <button onClick={nextWall} aria-label={t.next}>
                <I n="next" s={14} />
              </button>
            </div>
          </div>
        </aside>
        <main className="main">
          <div className="bar">
            <button
              className="ib"
              onClick={() => setLang(lang === "zh" ? "en" : "zh")}
              title={t.language}
            >
              <I n="globe" s={15} />
              <span>{lang === "zh" ? "EN" : "中"}</span>
            </button>
            <button
              className="ib"
              onClick={() => setTheme(theme === "light" ? "dark" : "light")}
              title={t.theme}
            >
              <I n={theme === "light" ? "moon" : "sun"} s={15} />
            </button>
          </div>
          <div className="scroll" key={page}>
            {body[page]}
          </div>
        </main>
      </div>

      <div className="taskbar">
        <button
          className={`tray-btn${tray ? " on" : ""}`}
          onClick={() => setTray(!tray)}
          aria-label={t.trayTitle}
        >
          <span className="logo sm">
            <I n="logo" s={12} />
          </span>
        </button>
        <I n="wifi" s={15} />
        <I n="volume" s={15} />
        <I n="battery" s={17} />
        <div className="clock">
          <b>14:32</b>
          <span>2025/6/18</span>
        </div>
      </div>

      {tray && (
        <div className="tray">
          <div className="tr-hero">
            <img src={cur.src} alt="" />
            <div className="tr-ov">
              <div className="tr-st">
                <span className={`dot${paused ? " off" : ""}`} />
                {paused ? t.pausedAll : t.playing}
              </div>
              <div className="tr-n">{name(cur)}</div>
              <div className="tr-m">
                {cur.spec} · {t.screens}
              </div>
            </div>
          </div>
          <div className="tr-ctl">
            <button className="pri" onClick={() => setPaused(!paused)}>
              <I n={paused ? "play" : "pause"} s={14} />
              {paused ? t.resume : t.pauseAll}
            </button>
            <button onClick={nextWall}>
              <I n="next" s={14} />
              {t.next}
            </button>
          </div>
          <div className="tr-sec">{t.recent}</div>
          <div className="tr-rec">
            {walls.slice(0, 4).map((w) => (
              <button
                key={w.id}
                className={w.id === cur.id ? "on" : ""}
                onClick={() => {
                  setAssigned([w.id, assigned[1]])
                  setToast(`${t.toastApplied} · ${name(w)}`)
                }}
              >
                <img src={w.src} alt={name(w)} />
              </button>
            ))}
          </div>
          <div className="tr-sec">{t.displays}</div>
          {[t.primary, t.secondary].map((d, i) => (
            <div key={d} className="tr-d">
              <I n="displays" s={14} />
              <span>{d}</span>
              <em>{name(walls.find((w) => w.id === assigned[i])!)}</em>
              <Toggle on={!paused} set={() => setPaused(!paused)} />
            </div>
          ))}
          <div className="tr-menu">
            <button
              onClick={() => {
                setPage("library")
                setTray(false)
              }}
            >
              <I n="library" s={14} />
              {t.openLibrary}
            </button>
            <button
              onClick={() => {
                setPage("general")
                setTray(false)
              }}
            >
              <I n="general" s={14} />
              {t.openSettings}
            </button>
            <button className="q">
              <I n="power" s={14} />
              {t.quit}
            </button>
          </div>
        </div>
      )}
      {toast && (
        <div className="toast">
          <I n="check" s={14} />
          {toast}
        </div>
      )}
    </div>
  )
}

function Library({ t, name, setToast, assigned, setAssigned }: Ctx) {
  const [f, setF] = useState<"all" | "video" | "image">("all")
  const [q, setQ] = useState("")
  const [sel, setSel] = useState(1)
  const [to, setTo] = useState("0")
  const list = walls.filter(
    (w) =>
      (f === "all" || w.type === f) &&
      (w.zh + w.en).toLowerCase().includes(q.toLowerCase()),
  )
  const w = walls.find((x) => x.id === sel)!
  const apply = () => {
    setAssigned(
      to === "2"
        ? [w.id, w.id]
        : to === "0"
          ? [w.id, assigned[1]]
          : [assigned[0], w.id],
    )
    setToast(`${t.toastApplied} · ${name(w)}`)
  }
  return (
    <div className="lib">
      <div className="lib-main">
        <Head
          title={t.library}
          sub={t.libSub}
          right={
            <div className="hr">
              <label className="srch">
                <I n="search" s={14} />
                <input
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  placeholder={t.search}
                />
              </label>
              <button className="btn pri">
                <I n="plus" s={14} />
                {t.import}
              </button>
            </div>
          }
        />
        <Seg
          value={f}
          set={setF}
          opts={[
            ["all", t.all],
            ["video", t.video],
            ["image", t.image],
          ]}
        />
        <div className="grid">
          {list.map((x) => (
            <button
              key={x.id}
              className={`tile${sel === x.id ? " on" : ""}`}
              onClick={() => setSel(x.id)}
              onDoubleClick={() => {
                setAssigned([x.id, assigned[1]])
                setToast(`${t.toastApplied} · ${name(x)}`)
              }}
            >
              <div className="th">
                <img src={x.src} alt="" />
                {assigned.includes(x.id) && (
                  <span className="use">{t.inUse}</span>
                )}
              </div>
              <div className="tl">
                <b>{name(x)}</b>
                <span>
                  {x.type === "video" ? t.video : t.image} · {x.spec}
                </span>
              </div>
            </button>
          ))}
        </div>
      </div>
      <aside className="insp">
        <img src={w.src} alt="" />
        <div className="insp-b">
        <h2>{name(w)}</h2>
        <p className="muted">
          {w.type === "video" ? t.video : t.image} · {w.spec}
        </p>
        <dl>
          <div>
            <dt>{t.source}</dt>
            <dd>{t.localFile}</dd>
          </div>
          <div>
            <dt>{t.resolution}</dt>
            <dd>3840 × 2160</dd>
          </div>
          <div>
            <dt>{t.added}</dt>
            <dd>{t.today} 09:42</dd>
          </div>
        </dl>
        <label className="lbl">{t.applyTo}</label>
        <select
          className="sel"
          value={to}
          onChange={(e) => setTo(e.target.value)}
        >
          <option value="0">{t.primary}</option>
          <option value="1">{t.secondary}</option>
          <option value="2">{t.allDisplays}</option>
        </select>
        <button className="btn pri wide" onClick={apply}>
          {t.apply}
        </button>
        </div>
      </aside>
    </div>
  )
}

function Stats({ items }: { items: [string, ReactNode, string?][] }) {
  return (
    <div className="stats">
      {items.map(([k, v, s]) => (
        <div key={k} className="stat">
          <span>{k}</span>
          <b>{v}</b>
          {s && <em>{s}</em>}
        </div>
      ))}
    </div>
  )
}

function Displays({ t, name, assigned, setAssigned }: Ctx) {
  const [d, setD] = useState(0)
  const [fit, setFit] = useState<"fill" | "fit" | "stretch">("fill")
  const [speed, setSpeed] = useState<[string, string]>(["1", "1"])
  const [prof, setProf] = useState<[string, string]>(["global", "saver"])
  const [off, setOff] = useState<[boolean, boolean]>([false, false])
  const [layout, setLayout] = useState<"ind" | "dup" | "span">("ind")
  const [remember, setRemember] = useState(true)
  const w = walls.find((x) => x.id === assigned[d])!
  const upd = <V,>(arr: [V, V], v: V): [V, V] =>
    d === 0 ? [v, arr[1]] : [arr[0], v]
  const paused = off[d]
  return (
    <div className="pg">
      <Head title={t.displays} sub={t.dispSub} />
      <div className="stage">
        {[0, 1].map((i) => (
          <button
            key={i}
            className={`mon m${i}${d === i ? " on" : ""}${off[i] ? " off" : ""}`}
            onClick={() => setD(i)}
          >
            <img
              src={
                walls.find((x) => x.id === assigned[layout === "ind" ? i : 0])!
                  .src
              }
              alt=""
            />
            <span>{i + 1}</span>
            {off[i] && (
              <i className="mon-p">
                <I n="pause" s={14} />
              </i>
            )}
          </button>
        ))}
      </div>
      <Group title={d === 0 ? t.primary : t.secondary}>
        <Row t={t.wallpaper}>
          <select
            className="sel"
            value={w.id}
            onChange={(e) => setAssigned(upd(assigned, +e.target.value))}
          >
            {walls.map((x) => (
              <option key={x.id} value={x.id}>
                {name(x)}
              </option>
            ))}
          </select>
        </Row>
        <Row t={t.fit}>
          <Seg
            value={fit}
            set={setFit}
            opts={[
              ["fill", t.fill],
              ["fit", t.fitMode],
              ["stretch", t.stretch],
            ]}
          />
        </Row>
        <Row t={t.speed} d={t.speedD}>
          <Seg
            value={speed[d]}
            set={(v) => setSpeed(upd(speed, v))}
            opts={[
              ["0.5", "0.5×"],
              ["0.75", "0.75×"],
              ["1", "1×"],
              ["1.5", "1.5×"],
            ]}
          />
        </Row>
        <Row t={t.perScreenProfile} d={t.perScreenProfileD}>
          <select
            className="sel"
            value={prof[d]}
            onChange={(e) => setProf(upd(prof, e.target.value))}
          >
            <option value="global">{t.followGlobal}</option>
            <option value="smooth">{t.smooth}</option>
            <option value="balanced">{t.balanced}</option>
            <option value="saver">{t.saver}</option>
          </select>
        </Row>
        <Row t={t.pauseScreen} d={t.pauseScreenD}>
          <Toggle on={paused} set={(v) => setOff(upd(off, v))} />
        </Row>
      </Group>
      <Group title={t.resStatus}>
        <Stats
          items={[
            [
              t.level,
              paused ? "L1" : prof[d] === "saver" ? "L2" : "L0",
              paused ? t.pausedAll : prof[d] === "saver" ? "15 fps" : t.live,
            ],
            [t.decode, "d3d11va", t.hwOk],
            [
              t.mode,
              d === 0 ? "2560 × 1440" : "1920 × 1080",
              d === 0 ? "144 Hz · 125%" : "60 Hz · 100%",
            ],
            [t.saved, d === 0 ? "6.2 h" : "9.8 h", "L1–L4"],
          ]}
        />
      </Group>
      <Group title={t.multi}>
        <Row t={t.layout} d={t.layoutD}>
          <Seg
            value={layout}
            set={setLayout}
            opts={[
              ["ind", t.independent],
              ["dup", t.duplicate],
              ["span", t.span],
            ]}
          />
        </Row>
        <Row t={t.remember} d={t.rememberD}>
          <Toggle on={remember} set={setRemember} />
        </Row>
      </Group>
    </div>
  )
}

const presets: Record<string, [string, string]> = {
  smooth: ["src", "100"],
  balanced: ["30", "75"],
  saver: ["15", "50"],
}

function Playback({ t, setToast }: { t: T; setToast: (s: string) => void }) {
  const [p, setP] = useState("balanced")
  const [fps, setFps] = useState("30")
  const [scale, setScale] = useState("75")
  const [hw, setHw] = useState(true)
  const [mute, setMute] = useState(true)
  const [resume, setResume] = useState(true)
  const [tc, setTc] = useState(true)
  const [tfps, setTfps] = useState("30")
  const [gif, setGif] = useState(true)
  const [cache, setCache] = useState("2.4 GB")
  const isCustom = presets[p][0] !== fps || presets[p][1] !== scale
  const pick = (id: string) => {
    setP(id)
    setFps(presets[id][0])
    setScale(presets[id][1])
  }
  const opts: [string, string, string][] = [
    ["smooth", t.smooth, t.smoothD],
    ["balanced", t.balanced, t.balancedD],
    ["saver", t.saver, t.saverD],
  ]
  return (
    <div className="pg">
      <Head title={t.playback} sub={t.playSub} />
      <Group title={t.profile}>
        <div className="cards">
          {opts.map(([id, a, b]) => (
            <button
              key={id}
              className={`opt${p === id && !isCustom ? " on" : ""}`}
              onClick={() => pick(id)}
            >
              <b>{a}</b>
              <span>{b}</span>
              {p === id && !isCustom && (
                <i className="ck">
                  <I n="check" s={12} />
                </i>
              )}
            </button>
          ))}
        </div>
        <Row t={t.fpsCap}>
          {isCustom && <span className="pill">{t.custom}</span>}
          <Seg
            value={fps}
            set={setFps}
            opts={[
              ["15", "15"],
              ["24", "24"],
              ["30", "30"],
              ["60", "60"],
              ["src", t.fpsSrc],
            ]}
          />
        </Row>
        <Row t={t.scale} d={t.scaleD}>
          <Seg
            value={scale}
            set={setScale}
            opts={[
              ["50", "50%"],
              ["75", "75%"],
              ["100", "100%"],
            ]}
          />
        </Row>
      </Group>
      <Group title={t.decoding}>
        <Row t={t.backend}>
          <select className="sel">
            <option>mpv</option>
            <option>Media Foundation</option>
          </select>
        </Row>
        <Row t={t.hw} d={t.hwD}>
          <Toggle on={hw} set={setHw} />
        </Row>
        <Row t={t.mute} d={t.muteD}>
          <Toggle on={mute} set={setMute} />
        </Row>
        <Row t={t.resumePos} d={t.resumePosD}>
          <Toggle on={resume} set={setResume} />
        </Row>
      </Group>
      <Group title={t.importGrp}>
        <Row t={t.transcode} d={t.transcodeD}>
          <Toggle on={tc} set={setTc} />
        </Row>
        <div className={`sub${tc ? "" : " dim"}`}>
          <Row t={t.targetRes}>
            <select className="sel" disabled={!tc}>
              <option>{t.matchDisplay}</option>
              <option>2160p</option>
              <option>1440p</option>
              <option>1080p</option>
            </select>
          </Row>
          <Row t={t.targetFps}>
            <Seg
              value={tfps}
              set={setTfps}
              opts={[
                ["24", "24"],
                ["30", "30"],
                ["60", "60"],
              ]}
            />
          </Row>
          <Row t={t.encoder}>
            <select className="sel" disabled={!tc}>
              <option>{t.sysEncoder}</option>
              <option>{t.swEncoder}</option>
            </select>
          </Row>
        </div>
        <Row t={t.gif} d={t.gifD}>
          <Toggle on={gif} set={setGif} />
        </Row>
        <Row t={t.cache} d={t.cacheD}>
          <span className="muted sm num-w">{cache}</span>
          <button
            className="btn"
            disabled={cache === "0 B"}
            onClick={() => {
              setCache("0 B")
              setToast(t.toastCleared)
            }}
          >
            {t.clear}
          </button>
        </Row>
      </Group>
    </div>
  )
}

function Rules({ t }: { t: T }) {
  const [fs, setFs] = useState(true)
  const [cv, setCv] = useState(true)
  const [mx, setMx] = useState(false)
  const [rd, setRd] = useState(true)
  const [scope, setScope] = useState<"one" | "all">("one")
  const [pm, setPm] = useState<"pause" | "stop">("pause")
  const [deb, setDeb] = useState("300")
  const [apps, setApps] = useState([
    ["obs64.exe", "pause"],
    ["Photoshop.exe", "saver"],
    ["mpc-hc64.exe", "ignore"],
  ])
  const [b, setB] = useState<"pause" | "fps" | "keep">("fps")
  const [low, setLow] = useState(20)
  const [lp, setLp] = useState(true)
  const [th, setTh] = useState(true)
  const extra = ["Steam.exe", "Figma.exe", "Blender.exe", "Code.exe"]
  return (
    <div className="pg">
      <Head title={t.rules} sub={t.rulesSub} />
      <Group title={t.autoPause}>
        <Row t={t.fullscreen} d={t.fullscreenD}>
          <Toggle on={fs} set={setFs} />
        </Row>
        <Row t={t.covered} d={t.coveredD}>
          <Toggle on={cv} set={setCv} />
        </Row>
        <Row t={t.maximized} d={t.maximizedD}>
          <Toggle on={mx} set={setMx} />
        </Row>
        <Row t={t.remote} d={t.remoteD}>
          <Toggle on={rd} set={setRd} />
        </Row>
        <Row t={t.lock} d={t.always}>
          <Toggle on disabled />
        </Row>
        <Row t={t.monitorOff} d={t.always}>
          <Toggle on disabled />
        </Row>
      </Group>
      <Group title={t.behavior}>
        <Row t={t.scope} d={t.scopeD}>
          <Seg
            value={scope}
            set={setScope}
            opts={[
              ["one", t.scopeOne],
              ["all", t.scopeAll],
            ]}
          />
        </Row>
        <Row t={t.pauseMode} d={t.pauseModeD}>
          <Seg
            value={pm}
            set={setPm}
            opts={[
              ["pause", t.pmPause],
              ["stop", t.pmStop],
            ]}
          />
        </Row>
        <Row t={t.debounce} d={t.debounceD}>
          <select
            className="sel sm"
            value={deb}
            onChange={(e) => setDeb(e.target.value)}
          >
            <option value="0">0 ms</option>
            <option value="300">300 ms</option>
            <option value="1000">1 s</option>
            <option value="3000">3 s</option>
          </select>
        </Row>
      </Group>
      <Group title={t.appRules}>
        {apps.map(([exe, ac], i) => (
          <div key={exe} className="row">
            <div className="app">
              <span className="app-i">{exe[0].toUpperCase()}</span>
              <code>{exe}</code>
            </div>
            <div className="rc">
              <select
                className="sel sm"
                value={ac}
                onChange={(e) =>
                  setApps(
                    apps.map((x, j) => (j === i ? [x[0], e.target.value] : x)),
                  )
                }
              >
                <option value="pause">{t.acPause}</option>
                <option value="saver">{t.acSaver}</option>
                <option value="ignore">{t.acIgnore}</option>
              </select>
              <button
                className="ib"
                aria-label="remove"
                onClick={() => setApps(apps.filter((_, j) => j !== i))}
              >
                <I n="x" s={14} />
              </button>
            </div>
          </div>
        ))}
        <div className="row">
          <span className="rd">{t.appRulesD}</span>
          <button
            className="btn"
            disabled={apps.length >= 3 + extra.length}
            onClick={() => {
              const n = extra.find((e) => !apps.some((a) => a[0] === e))
              if (n) setApps([...apps, [n, "pause"]])
            }}
          >
            <I n="plus" s={14} />
            {t.addApp}
          </button>
        </div>
      </Group>
      <Group title={t.battery}>
        <Row t={t.battery}>
          <Seg
            value={b}
            set={setB}
            opts={[
              ["pause", t.bPause],
              ["fps", t.bFps],
              ["keep", t.bKeep],
            ]}
          />
        </Row>
        <Row t={t.lowBat} d={t.lowBatD}>
          <input
            type="range"
            className="rng"
            min={5}
            max={50}
            step={5}
            value={low}
            onChange={(e) => setLow(+e.target.value)}
          />
          <span className="num">{low}%</span>
        </Row>
        <Row t={t.lowPower} d={t.lowPowerD}>
          <Toggle on={lp} set={setLp} />
        </Row>
        <Row t={t.thermal} d={t.thermalD}>
          <Toggle on={th} set={setTh} />
        </Row>
      </Group>
    </div>
  )
}

function General({
  t,
  lang,
  setLang,
  theme,
  setTheme,
  accent,
  setAccent,
}: {
  t: T
  lang: Lang
  setLang: (l: Lang) => void
  theme: Theme
  setTheme: (t: Theme) => void
  accent: string
  setAccent: (a: string) => void
}) {
  const [launch, setLaunch] = useState(false)
  const [tray, setTray] = useState(true)
  const [restore, setRestore] = useState(true)
  return (
    <div className="pg">
      <Head title={t.general} sub={t.genSub} />
      <Group title={t.appearance}>
        <Row t={t.theme}>
          <div className="themes">
            {(["light", "dark"] as const).map((m) => (
              <button
                key={m}
                className={`tp ${m}${theme === m ? " on" : ""}`}
                onClick={() => setTheme(m)}
              >
                <span className="tpv">
                  <i />
                  <i />
                </span>
                {m === "light" ? t.light : t.dark}
              </button>
            ))}
          </div>
        </Row>
        <Row t={t.accent}>
          <div className="sw">
            {accents.map((a) => (
              <button
                key={a}
                style={{ background: a }}
                className={accent === a ? "on" : ""}
                onClick={() => setAccent(a)}
                aria-label={a}
              />
            ))}
          </div>
        </Row>
        <Row t={t.language}>
          <Seg
            value={lang}
            set={setLang}
            opts={[
              ["zh", "简体中文"],
              ["en", "English"],
            ]}
          />
        </Row>
      </Group>
      <Group title={t.startup}>
        <Row t={t.launch} d={t.launchD}>
          <Toggle on={launch} set={setLaunch} />
        </Row>
        <Row t={t.tray} d={t.trayD}>
          <Toggle on={tray} set={setTray} />
        </Row>
        <Row t={t.restoreLast} d={t.restoreLastD}>
          <Toggle on={restore} set={setRestore} />
        </Row>
      </Group>
      <Group title={t.shortcuts}>
        {(
          [
            [t.scPause, ["Ctrl", "Alt", "P"]],
            [t.scNext, ["Ctrl", "Alt", "→"]],
            [t.scOpen, ["Ctrl", "Alt", "W"]],
          ] as [string, string[]][]
        ).map(([l, keys]) => (
          <Row key={l} t={l}>
            <button className="keys">
              {keys.map((k) => (
                <kbd key={k}>{k}</kbd>
              ))}
            </button>
          </Row>
        ))}
      </Group>
      <Group title={t.storage}>
        {[
          [t.libPath, "%LOCALAPPDATA%\\Wallpaper\\library"],
          [t.cachePath, "%LOCALAPPDATA%\\Wallpaper\\cache"],
        ].map(([l, p]) => (
          <Row key={l} t={l} d={p}>
            <button className="btn ghost">
              <I n="folder" s={14} />
            </button>
            <button className="btn">{t.change}</button>
          </Row>
        ))}
      </Group>
    </div>
  )
}

function Versions({
  t,
  lang,
  setToast,
}: {
  t: T
  lang: Lang
  setToast: (s: string) => void
}) {
  const [ch, setCh] = useState<"stable" | "beta" | "nightly">("stable")
  const [busy, setBusy] = useState(false)
  const [au, setAu] = useState(true)
  const [ai, setAi] = useState(false)
  const [keep, setKeep] = useState("2")
  const check = () => {
    setBusy(true)
    setTimeout(() => {
      setBusy(false)
      setToast(t.toastUpToDate)
    }, 1200)
  }
  return (
    <div className="pg">
      <Head title={t.versions} sub={t.verSub} />
      <div className="ver-hero">
        <span className="logo lg">
          <I n="logo" s={24} />
        </span>
        <div className="vh-t">
          <div className="muted sm">{t.current}</div>
          <div className="vh-v">
            0.2.1 <span className="pill">{t[ch]}</span>
          </div>
          <div className="ok-t">
            <I n="check" s={13} />
            {t.latest}
          </div>
        </div>
        <button className="btn" onClick={check} disabled={busy}>
          {busy ? <span className="spin" /> : <I n="versions" s={14} />}
          {busy ? t.checking : t.check}
        </button>
      </div>
      <Group>
        <Row t={t.channel}>
          <Seg
            value={ch}
            set={setCh}
            opts={[
              ["stable", t.stable],
              ["beta", t.beta],
              ["nightly", t.nightly],
            ]}
          />
        </Row>
        <Row t={t.autoUpdate} d={t.autoUpdateD}>
          <Toggle on={au} set={setAu} />
        </Row>
        <Row t={t.autoInstall} d={t.autoInstallD}>
          <Toggle on={ai} set={setAi} />
        </Row>
        <Row t={t.keep} d={t.keepD}>
          <select
            className="sel sm"
            value={keep}
            onChange={(e) => setKeep(e.target.value)}
          >
            <option>1</option>
            <option>2</option>
            <option>3</option>
          </select>
        </Row>
      </Group>
      <Group title={t.history}>
        <div className="tl-list">
          {versions.map((v, i) => (
            <div key={v.v} className={`tli${i === 0 ? " cur" : ""}`}>
              <span className="tl-dot" />
              <div className="tl-b">
                <div className="tl-h">
                  <b>v{v.v}</b>
                  <span className="muted sm">{v.date}</span>
                  {i === 0 && <span className="pill ok">{t.installed}</span>}
                </div>
                <p>{v[lang]}</p>
              </div>
              {i > 0 && i <= +keep && (
                <button
                  className="btn ghost"
                  onClick={() => setToast(`${t.toastRolled} · v${v.v}`)}
                >
                  {t.rollback}
                </button>
              )}
            </div>
          ))}
        </div>
        <p className="foot">{t.delta}</p>
      </Group>
    </div>
  )
}

function About({ t, setToast }: { t: T; setToast: (s: string) => void }) {
  const [dbg, setDbg] = useState(false)
  const [tel, setTel] = useState(false)
  const [cr, setCr] = useState<"ask" | "auto" | "never">("ask")
  const [id, setId] = useState("7f3a9c21")
  return (
    <div className="pg">
      <Head title={t.about} sub={t.aboutSub} />
      <div className="ver-hero">
        <span className="logo lg">
          <I n="logo" s={24} />
        </span>
        <div className="vh-t">
          <div className="vh-v">{t.app}</div>
          <div className="muted sm">0.2.1 · windows/amd64</div>
          <p className="muted sm ab">{t.aboutD}</p>
        </div>
      </div>
      <Group title={t.metrics}>
        <Stats
          items={[
            [t.saved, "16.0 h", t.metricsD],
            [t.pauseLat, "98.6%", "p95 · 212 ms"],
            [t.hwRate, "100%", "d3d11va"],
            [t.crashes, "0", "mpv · 7d"],
          ]}
        />
      </Group>
      <Group title={t.diag}>
        <Row t={t.export} d={t.exportD}>
          <button className="btn" onClick={() => setToast(t.toastExported)}>
            <I n="download" s={14} />
            {t.export}
          </button>
        </Row>
        <Row t={t.logs} d="%LOCALAPPDATA%\Wallpaper\logs">
          <button className="btn">
            <I n="folder" s={14} />
            {t.logs}
          </button>
        </Row>
        <Row t={t.debugMode} d={t.debugModeD}>
          <Toggle on={dbg} set={setDbg} />
        </Row>
      </Group>
      <Group title={t.privacyGrp}>
        <Row t={t.telemetry} d={t.telemetryD}>
          <Toggle on={tel} set={setTel} />
        </Row>
        <Row t={t.crash}>
          <Seg
            value={cr}
            set={setCr}
            opts={[
              ["ask", t.crashAsk],
              ["auto", t.crashAuto],
              ["never", t.crashNever],
            ]}
          />
        </Row>
        <Row t={t.installId} d={t.installIdD}>
          <code className="mono">{id}</code>
          <button
            className="btn"
            onClick={() => {
              setId(Math.random().toString(16).slice(2, 10))
              setToast(t.toastReset)
            }}
          >
            {t.reset}
          </button>
        </Row>
      </Group>
      <Group title={t.licenses}>
        {[
          ["MyGo", "MIT"],
          ["purego", "Apache-2.0"],
          ["mpv", "GPL / LGPL"],
        ].map(([a, b]) => (
          <Row key={a} t={a}>
            <span className="muted sm">{b}</span>
          </Row>
        ))}
      </Group>
    </div>
  )
}
