; Windows 安装器（D-06：NSIS）。按用户安装到 %LOCALAPPDATA%\Programs\Wallpaper，不需要管理员权限。
; makensis -DVERSION=0.1.0 -DARCH=amd64 -DSRC=dist/windows-amd64/ -DOUT=dist/Wallpaper-0.1.0-windows-amd64-setup.exe packaging/windows/installer.nsi
; SRC 必须以路径分隔符结尾（package-windows.sh 会按平台追加）；SRC 下需要 wallpaperd.exe 和发布/依赖许可说明。本轮不捆绑 mpv。
Unicode true
!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef ARCH
  !define ARCH "amd64"
!endif
!ifndef SRC
  !error "pass -DSRC=dist/windows-<arch>"
!endif
!ifndef OUT
  !define OUT "Wallpaper-${VERSION}-windows-${ARCH}-setup.exe"
!endif
!define APPNAME "Wallpaper"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"

; package-windows.sh passes an absolute path; this fallback keeps a manual
; makensis invocation from the repository root working too.
!ifndef ICON
  !define ICON "${__FILEDIR__}../../assets/icon/wallpaper.ico"
!endif
!if /FileExists "${ICON}"
!else
  !error "icon not found: ${ICON} (pass -DICON=<absolute path>)"
!endif
Icon "${ICON}"
UninstallIcon "${ICON}"

Name "${APPNAME} ${VERSION}"
OutFile "${OUT}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APPNAME}"
InstallDirRegKey HKCU "${UNINSTKEY}" "InstallLocation"
SetCompressor /SOLID lzma
!include "LogicLib.nsh"
!include "MUI2.nsh"
!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"
!define MUI_WELCOMEPAGE_TITLE "Wallpaper ${VERSION} (${ARCH})"
!define MUI_WELCOMEPAGE_TEXT "Native ${ARCH} application. Use the installer matching your Windows architecture.$\r$\n$\r$\nThis developer release does not bundle mpv. Default video playback requires a separately installed mpv; set its executable path in Playback & Performance, under Decode & render.$\r$\n$\r$\nRead RELEASE-NOTES.txt in the install folder for signing, runtime and validation limits."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${SRC}LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\wallpaperd.exe"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

; Refuse to install an amd64 binary on arm64 Windows or vice versa.
; The NSIS bootstrap itself is 32-bit x86, so query the native machine via
; IsWow64Process2 and fall back to PROCESSOR_ARCHITEW6432 / PROCESSOR_ARCHITECTURE.
Function .onInit
  StrCpy $0 ""
  System::Call 'kernel32::IsWow64Process2(p -1, *i 0 r1, *i 0 r2) i.r3'
  ${If} $3 != "error"
  ${AndIf} $3 <> 0
    IntOp $2 $2 & 0xFFFF
    ${If} $2 = 0x8664
      StrCpy $0 "amd64"
    ${ElseIf} $2 = 0xAA64
      StrCpy $0 "arm64"
    ${Else}
      StrCpy $0 "unsupported"
    ${EndIf}
  ${Else}
    ReadEnvStr $1 "PROCESSOR_ARCHITEW6432"
    ${If} $1 == ""
      ReadEnvStr $1 "PROCESSOR_ARCHITECTURE"
    ${EndIf}
    ${If} $1 == "AMD64"
      StrCpy $0 "amd64"
    ${ElseIf} $1 == "ARM64"
      StrCpy $0 "arm64"
    ${Else}
      StrCpy $0 "unsupported"
    ${EndIf}
  ${EndIf}
  ${If} $0 != "${ARCH}"
    MessageBox MB_ICONSTOP|MB_OK "This installer is for Windows ${ARCH}, but this machine is $0. Please run the matching installer." /SD IDOK
    Abort
  ${EndIf}
FunctionEnd

; Terminate wallpaperd.exe and its child process tree, then poll until the
; executable file lock is released (up to 5 seconds) before overwriting/deleting.
!macro StopWallpaperd
  !define _STOP_ID ${__LINE__}
  nsExec::Exec 'taskkill /F /T /IM wallpaperd.exe'
  Pop $0
  StrCpy $1 0
  ${DoWhile} $1 < 25
    IfFileExists "$INSTDIR\wallpaperd.exe" +2 0
    Goto _stop_done_${_STOP_ID}
    ClearErrors
    FileOpen $2 "$INSTDIR\wallpaperd.exe" a
    ${IfNot} ${Errors}
      FileClose $2
      Goto _stop_done_${_STOP_ID}
    ${EndIf}
    Sleep 200
    IntOp $1 $1 + 1
  ${Loop}
  _stop_done_${_STOP_ID}:
  !undef _STOP_ID
!macroend

Section "Install"
  ; 升级时先结束正在运行的实例及子进程树，并等待可执行文件锁释放
  !insertmacro StopWallpaperd
  ; Clear stale updater transaction state so an awaiting rollback cannot
  ; downgrade a freshly installed version on next launch.
  RMDir /r "$INSTDIR\.update"
  SetOutPath "$INSTDIR"
  File "${SRC}wallpaperd.exe"
  File "${SRC}RELEASE-NOTES.txt"
  File "${SRC}BUILD-INFO.txt"
  File "${SRC}THIRD-PARTY-NOTICES.txt"
  File "${SRC}LICENSE"
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortCut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\wallpaperd.exe" "" "$INSTDIR\wallpaperd.exe" 0
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\wallpaperd.exe,0"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  !insertmacro StopWallpaperd
  ; 开机自启项由 MyGo 写在 HKCU\...\Run（值名 = 应用名）
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPNAME}"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run" "${APPNAME}"
  Delete "$SMPROGRAMS\${APPNAME}.lnk"
  ; Remove updater transaction state; do NOT delete $INSTDIR\mpv because mpv is
  ; user-managed and not bundled by this installer.
  RMDir /r "$INSTDIR\.update"
  Delete "$INSTDIR\wallpaperd.exe"
  Delete "$INSTDIR\RELEASE-NOTES.txt"
  Delete "$INSTDIR\BUILD-INFO.txt"
  Delete "$INSTDIR\THIRD-PARTY-NOTICES.txt"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "${UNINSTKEY}"
  ; 用户数据（%APPDATA%\WallpaperApp、%LOCALAPPDATA%\WallpaperApp）保留，避免误删壁纸库
SectionEnd
