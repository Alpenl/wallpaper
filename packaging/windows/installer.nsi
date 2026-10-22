; Windows 安装器（D-06：NSIS）。按用户安装到 %LOCALAPPDATA%\Programs\Wallpaper，不需要管理员权限。
; makensis -DVERSION=0.1.0 -DARCH=amd64 -DSRC=dist/windows-amd64 -DOUT=dist/Wallpaper-0.1.0-windows-amd64-setup.exe packaging/windows/installer.nsi
; SRC 下需要 wallpaperd.exe 和发布/依赖许可说明。本轮不捆绑 mpv。
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

Name "${APPNAME} ${VERSION}"
OutFile "${OUT}"
RequestExecutionLevel user
InstallDir "$LOCALAPPDATA\Programs\${APPNAME}"
SetCompressor /SOLID lzma
!include "MUI2.nsh"
!define MUI_WELCOMEPAGE_TITLE "Wallpaper ${VERSION} (${ARCH})"
!define MUI_WELCOMEPAGE_TEXT "Native ${ARCH} application. Use the installer matching your Windows architecture.$\r$\n$\r$\nThis developer release does not bundle mpv. Default video playback requires a separately installed mpv; set its executable path in Playback & Performance, under Decode & render.$\r$\n$\r$\nRead RELEASE-NOTES.txt in the install folder for signing, runtime and validation limits."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${SRC}/LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\wallpaperd.exe"
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Section "Install"
  ; 升级时先结束正在运行的实例（mpv 子进程在 Job Object 里，会一起结束）
  nsExec::Exec 'taskkill /F /IM wallpaperd.exe'
  SetOutPath "$INSTDIR"
  File "${SRC}/wallpaperd.exe"
  File "${SRC}/RELEASE-NOTES.txt"
  File "${SRC}/BUILD-INFO.txt"
  File "${SRC}/THIRD-PARTY-NOTICES.txt"
  File "${SRC}/LICENSE"
  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  CreateShortCut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\wallpaperd.exe"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKCU "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTKEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
  nsExec::Exec 'taskkill /F /IM wallpaperd.exe'
  ; 开机自启项由 MyGo 写在 HKCU\...\Run（值名 = 应用名）
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPNAME}"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run" "${APPNAME}"
  Delete "$SMPROGRAMS\${APPNAME}.lnk"
  RMDir /r "$INSTDIR\mpv"
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
