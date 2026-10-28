; GoBackup Windows Agent — installer
; Builds a single setup.exe that installs:
;   - gobackup-winagent-tray.exe (desktop app: tray icon + local config page)
;   - gobackup-winagent.exe      (CLI + the actual Windows Service binary)
; and registers gobackup-winagent as a real Windows Service (Local System,
; auto-start) so backups and the Central connection keep working even with
; nobody logged into the machine — the tray app is just a convenience UI
; for whoever IS logged in, launched at every login, but it no longer does
; the real work itself (see cmd/winagent-tray/main.go).
;
; Build (from Linux or Windows, with makensis installed):
;   makensis installer/gobackup-agent.nsi

!define APP_NAME "GoBackup Agent"
!define APP_DIR "GoBackup"
!define PUBLISHER "GoBackup"
!define UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\GoBackupAgent"

Name "${APP_NAME}"
OutFile "gobackup-agent-setup.exe"
InstallDir "$PROGRAMFILES64\${APP_DIR}"
InstallDirRegKey HKLM "Software\${APP_DIR}" "InstallDir"
RequestExecutionLevel admin
SetCompressor /SOLID lzma

!include "MUI2.nsh"

!define MUI_ABORTWARNING
!define MUI_ICON "icons\gobackup.ico"
!define MUI_UNICON "icons\gobackup.ico"

; Branding: tall artwork on the Welcome/Finish pages, small logo+wordmark
; in the header strip of every other page.
!define MUI_WELCOMEFINISHPAGE_BITMAP "icons\welcome.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "icons\welcome.bmp"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_BITMAP "icons\header.bmp"
!define MUI_HEADERIMAGE_UNBITMAP "icons\header.bmp"

!define MUI_WELCOMEPAGE_TITLE "GoBackup"
!define MUI_WELCOMEPAGE_TEXT "Backup inteligente. Dados protegidos.$\r$\n$\r$\nDesenvolvido por Cleber Reis.$\r$\n$\r$\nEste assistente vai instalar o GoBackup Agent neste computador. Clique em Avançar para continuar."

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\gobackup-winagent-tray.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Abrir o GoBackup agora (ícone na bandeja)"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "PortugueseBR"

Section "Instalar" SEC01
    ; Write to the native 64-bit registry view — without this, a 32-bit
    ; NSIS installer gets silently redirected to
    ; HKLM\SOFTWARE\WOW6432Node\... by WOW64. Windows autostart processing
    ; happens to check that view too (by design, so 32-bit installers
    ; still work), so this isn't a functional bug, but writing natively
    ; avoids the split and keeps InstallDirRegKey reads consistent with
    ; what Explorer/regedit show by default.
    SetRegView 64

    SetOutPath "$INSTDIR"
    File "gobackup-winagent.exe"
    File "gobackup-winagent-tray.exe"
    File "icons\gobackup.ico"

    ; Autostart: launch the tray app for every user who logs in (status
    ; display + config UI only now — the real work happens in the Windows
    ; Service installed below, regardless of whether anyone ever logs in).
    ; HKLM Run (not HKCU) so it works regardless of which user installed it.
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "GoBackupAgent" '"$INSTDIR\gobackup-winagent-tray.exe"'

    ; Start Menu shortcut
    CreateDirectory "$SMPROGRAMS\${APP_NAME}"
    CreateShortCut "$SMPROGRAMS\${APP_NAME}\GoBackup Agent.lnk" "$INSTDIR\gobackup-winagent-tray.exe" "" "$INSTDIR\gobackup.ico"
    CreateShortCut "$SMPROGRAMS\${APP_NAME}\Desinstalar.lnk" "$INSTDIR\uninstall.exe" "" "$INSTDIR\gobackup.ico"

    ; Uninstall registration (shows up in "Aplicativos e recursos")
    WriteRegStr HKLM "${UNINST_KEY}" "DisplayName" "${APP_NAME}"
    WriteRegStr HKLM "${UNINST_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
    WriteRegStr HKLM "${UNINST_KEY}" "InstallLocation" "$INSTDIR"
    WriteRegStr HKLM "${UNINST_KEY}" "Publisher" "${PUBLISHER}"
    WriteRegStr HKLM "${UNINST_KEY}" "DisplayIcon" "$INSTDIR\gobackup.ico"
    WriteRegStr HKLM "Software\${APP_DIR}" "InstallDir" "$INSTDIR"

    WriteUninstaller "$INSTDIR\uninstall.exe"

    ; Register + start the real Windows Service — this is what makes
    ; backups and the Central connection survive a reboot with nobody
    ; logged in. Runs after the uninstaller is written so a failure here
    ; still leaves the install in a cleanly uninstallable state.
    nsExec::ExecToLog '"$INSTDIR\gobackup-winagent.exe" service-install'
SectionEnd

Section "Uninstall"
    SetRegView 64
    ; Stop and remove the Windows Service FIRST — otherwise the service
    ; keeps gobackup-winagent.exe open and the Delete below fails.
    nsExec::ExecToLog '"$INSTDIR\gobackup-winagent.exe" service-uninstall'

    ; Stop a running tray instance before removing its file (Windows
    ; refuses to delete the .exe of a process that's still executing).
    nsExec::Exec 'taskkill /F /IM gobackup-winagent-tray.exe'

    DeleteRegValue HKLM "Software\Microsoft\Windows\CurrentVersion\Run" "GoBackupAgent"
    DeleteRegKey HKLM "${UNINST_KEY}"
    DeleteRegKey HKLM "Software\${APP_DIR}"

    Delete "$INSTDIR\gobackup-winagent.exe"
    Delete "$INSTDIR\gobackup-winagent-tray.exe"
    Delete "$INSTDIR\gobackup.ico"
    Delete "$INSTDIR\uninstall.exe"
    RMDir "$INSTDIR"

    Delete "$SMPROGRAMS\${APP_NAME}\GoBackup Agent.lnk"
    Delete "$SMPROGRAMS\${APP_NAME}\Desinstalar.lnk"
    RMDir "$SMPROGRAMS\${APP_NAME}"

    ; Deliberately NOT removing C:\ProgramData\GoBackup (config.json/
    ; jobs.json) — reinstalling should pick the existing registration back
    ; up rather than forcing the user to register again.
SectionEnd
