package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	fetch "github.com/egoist/mygo/internal/download"
	"github.com/egoist/mygo/internal/nsis"
)

// The Windows installer is built with NSIS: a per-user install in
// %LOCALAPPDATA%\Programs, where the app can update itself, with a Start
// menu shortcut, a desktop shortcut the finish page offers, and an
// uninstaller that Settings > Apps lists. Windows rarely has NSIS
// installed, so there mygo build downloads it when it needs it, as Tauri
// and electron-builder do; elsewhere makensis must be installed.

// makensis finds the NSIS compiler: an installed one or, on Windows, the
// one an earlier build downloaded.
func makensis() string {
	if p, err := exec.LookPath("makensis"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles"} {
			if p := filepath.Join(os.Getenv(env), "NSIS", "makensis.exe"); fileExists(p) {
				return p
			}
		}
		if dir := nsis.Dir(); dir != "" {
			return nsis.Executable(dir)
		}
	}
	return ""
}

// nsisCompiler returns the NSIS compiler, which it downloads on Windows
// when there is none, or "" on other systems without NSIS.
func nsisCompiler() (string, error) {
	if p := makensis(); p != "" || runtime.GOOS != "windows" {
		return p, nil
	}
	dir := nsis.Dir()
	if dir == "" {
		return "", errors.New("no cache directory to download NSIS into: install NSIS (makensis)")
	}
	return downloadNSIS(dir)
}

// downloadNSIS downloads nsis.Release into dir, unless it is there
// already, and returns its makensis.
func downloadNSIS(dir string) (string, error) {
	if p := nsis.Executable(dir); p != "" {
		return p, nil
	}
	v := nsis.Release.Version
	t := con.start("Downloading NSIS " + v + " for the Windows installer")
	p, err := nsis.Download(context.Background(), dir, func(url string) fetch.File { return downloadFile(t, url, "") })
	if t.end(err, "Downloaded NSIS "+v) != nil {
		return "", fmt.Errorf("downloading NSIS %s (or install it: https://nsis.sourceforge.io):\n%w", v, err)
	}
	return p, nil
}

// downloadHeaderTimeout is how long the CLI's downloads wait for a host to
// answer, so that one that takes connections and never answers gives way
// to the next copy.
var downloadHeaderTimeout = 30 * time.Second

// downloadFile is a download by the CLI of url, whose SHA-256 is sum: its
// progress shows on t, and a host that does not answer within
// downloadHeaderTimeout fails it.
func downloadFile(t *task, url, sum string) fetch.File {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = downloadHeaderTimeout
	return fetch.File{
		URL:    url,
		SHA256: sum,
		Client: &http.Client{Transport: transport, Timeout: 10 * time.Minute},
		Header: http.Header{"User-Agent": {"mygo/" + version}},
		Progress: func(total int64) io.Writer {
			return &downloadProgress{t: t, total: total}
		},
	}
}

// installerFileName is "<slug>-setup-<version>-<arch>.exe", as
// "my-app-setup-1.2.0-amd64.exe": each architecture's installer is named
// apart, so that the builds of two, uploaded to one release or bucket,
// keep both. install.ps1 finds it by installerPrefix.
func installerFileName(c *Config, goarch string) string {
	return installerPrefix(c) + fsName(c.Version) + "-" + goarch + ".exe"
}

// installerPrefix is what the names of the app's installers start with,
// before their version.
func installerPrefix(c *Config) string { return c.slug() + installerMark }

// installerMark follows the slug in the names of installers, which
// uploads publishes.
const installerMark = "-setup-"

// writeInstaller builds installerFileName in stage from the app files
// installed, built for goarch, the executable exe among them, and returns
// its path, or "" without NSIS.
func writeInstaller(c *Config, stage, work, exe, goarch string, installed []string) (string, error) {
	tool, err := nsisCompiler()
	if err != nil {
		return "", err
	}
	if tool == "" {
		warnf("Skipping the Windows installer: install NSIS (makensis)")
		return "", nil
	}
	out := filepath.Join(stage, installerFileName(c, goarch))
	var files strings.Builder
	for _, name := range installed {
		p := filepath.Join(stage, name)
		if isDir(p) {
			fmt.Fprintf(&files, "  File /r %s\n", nsisString(p))
		} else {
			fmt.Fprintf(&files, "  File %s\n", nsisString(p))
		}
	}
	icon := ""
	if c.Icon != "" {
		src, err := os.ReadFile(c.path(c.Icon))
		if err != nil {
			return "", err
		}
		ico, err := pngToICO(src)
		if err != nil {
			return "", err
		}
		path := filepath.Join(work, "installer.ico")
		if err := os.WriteFile(path, ico, 0o644); err != nil {
			return "", err
		}
		icon = "!define MUI_ICON " + nsisString(path) + "\n!define MUI_UNICON " + nsisString(path) + "\n"
	}
	signing, env, err := uninstallerSigning(c, tool)
	if err != nil {
		return "", err
	}
	script, err := installerScript(c, out, exe, files.String(), icon, signing)
	if err != nil {
		return "", err
	}
	nsi := filepath.Join(work, "installer.nsi")
	if err := writeNSIS(nsi, script); err != nil {
		return "", err
	}
	t := con.start("Creating " + filepath.Base(out))
	if signing != "" {
		t.set("signing Uninstall.exe")
	}
	cmd := exec.Command(tool, "-V2", nsi)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.fail()
		return "", fmt.Errorf("makensis: %v\n%s", err, out)
	}
	t.done("Created " + filepath.Base(out))
	return out, nil
}

// installerScript returns the NSIS script of the installer out, which
// installs the files that the File commands files list, the executable exe
// among them, with the icon and signing commands of writeInstaller.
func installerScript(c *Config, out, exe, files, icon, signing string) (string, error) {
	uninstallKey := `Software\Microsoft\Windows\CurrentVersion\Uninstall\` + c.Identifier
	shortcut := nsisEscape(c.shortcutName()) + ".lnk"
	register, unregister := nsisAssociations(c, exe)
	langs, err := c.languages()
	if err != nil {
		return "", err
	}
	languages, err := nsisLanguageMacros(langs)
	if err != nil {
		return "", err
	}
	return `Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma
` + nsisName(c.windowsName()) + `
OutFile ` + nsisString(out) + `
` + signing + `InstallDir "$LOCALAPPDATA\Programs\` + nsisEscape(fsName(c.Name)) + `"
RequestExecutionLevel user
BrandingText " "
` + icon + `!define MUI_FINISHPAGE_RUN "$INSTDIR\` + nsisEscape(exe) + `"
!define MUI_FINISHPAGE_SHOWREADME
!define MUI_FINISHPAGE_SHOWREADME_TEXT "Create a desktop shortcut"
!define MUI_FINISHPAGE_SHOWREADME_FUNCTION CreateDesktopShortcut
!include "MUI2.nsh"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
` + languages + `
` + nsisVersionInfo(c) + nsisCloseApp(c) + `
Section
  Push "$INSTDIR"
  Call CloseApp
  SetOutPath "$INSTDIR"
` + files + `  WriteUninstaller "$INSTDIR\Uninstall.exe"
` + nsisRenamedShortcuts(c) + `  CreateShortCut "$SMPROGRAMS\` + shortcut + `" "$INSTDIR\` + nsisEscape(exe) + `"
` + nsisUninstallEntry(c, uninstallKey, exe) + register + `SectionEnd

; The finish page offers it, so silent installs (/S) make none.
Function CreateDesktopShortcut
  SetOutPath "$INSTDIR"
  CreateShortCut "$DESKTOP\` + shortcut + `" "$INSTDIR\` + nsisEscape(exe) + `"
FunctionEnd

; Deletes the shortcut on the stack, trying again for 5 s while it is in
; use: Explorer opens a new shortcut a few seconds after it appears, not
; sharing it for deletion, and Delete fails meanwhile. It succeeds when
; there is no shortcut.
Function un.DeleteShortcut
  Exch $0
  Push $1
  StrCpy $1 50
  retry:
    ClearErrors
    Delete $0
    IfErrors 0 done
    IntOp $1 $1 - 1
    IntCmp $1 0 done
    Sleep 100
    Goto retry
  done:
  Pop $1
  Pop $0
FunctionEnd

Section "Uninstall"
  Push "$INSTDIR"
  Call un.CloseApp
  Push "$SMPROGRAMS\` + shortcut + `"
  Call un.DeleteShortcut
  Push "$DESKTOP\` + shortcut + `"
  Call un.DeleteShortcut
  DeleteRegKey HKCU ` + nsisString(uninstallKey) + `
` + unregister + `  ; Last, so that the app's folder is gone once the uninstall is done:
  ; the uninstaller runs from a copy of itself that nothing waits for.
  RMDir /r "$INSTDIR"
SectionEnd
`, nil
}

// nsisName returns the Name command of the installer, which titles its
// pages: name, and the same with doubled ampersands where it has any, which
// the texts of buttons and labels need lest "&" underline the next letter.
func nsisName(name string) string {
	if !strings.Contains(name, "&") {
		return "Name " + nsisString(name)
	}
	return "Name " + nsisString(name) + " " + nsisString(strings.ReplaceAll(name, "&", "&&"))
}

// nsisRenamedShortcuts returns the installer commands that remove the
// shortcuts an earlier version made under the name when windows.displayName
// names them otherwise: the Start menu's goes, and a desktop one is made
// again under the new name, as the user chose to have one.
func nsisRenamedShortcuts(c *Config) string {
	old := c.executableName()
	if strings.EqualFold(old, c.shortcutName()) {
		return ""
	}
	old = nsisEscape(old) + ".lnk"
	return `  Delete "$SMPROGRAMS\` + old + `"
  IfFileExists "$DESKTOP\` + old + `" 0 +3
  Delete "$DESKTOP\` + old + `"
  Call CreateDesktopShortcut
`
}

// nsisUninstallEntry returns the installer commands that write the entry
// of the app in Settings > Apps, under uninstallKey.
func nsisUninstallEntry(c *Config, uninstallKey, exe string) string {
	var b strings.Builder
	str := func(name, value string) {
		fmt.Fprintf(&b, "  WriteRegStr HKCU %s %s %s\n", nsisString(uninstallKey), nsisString(name), value)
	}
	str("DisplayName", nsisString(c.windowsName()))
	str("DisplayVersion", nsisString(c.Version))
	str("DisplayIcon", `"$INSTDIR\`+nsisEscape(exe)+`"`)
	if p := c.publisher(); p != "" {
		str("Publisher", nsisString(p))
	}
	str("InstallLocation", `"$INSTDIR"`)
	str("UninstallString", `'"$INSTDIR\Uninstall.exe"'`)
	str("QuietUninstallString", `'"$INSTDIR\Uninstall.exe" /S'`)
	fmt.Fprintf(&b, "  WriteRegDWORD HKCU %s \"NoModify\" 1\n", nsisString(uninstallKey))
	fmt.Fprintf(&b, "  WriteRegDWORD HKCU %s \"NoRepair\" 1\n", nsisString(uninstallKey))
	return b.String()
}

// nsisCloseApp returns the installer and uninstaller functions CloseApp,
// which quit the app running from the folder on the stack before its
// files are replaced or deleted: they ask the windows of its processes,
// those whose executables are in the folder, to close and, after 10 s,
// end those still running, as an app in the tray or menu bar keeps
// running without windows. A silent install or uninstall (/S), as an
// update runs, quits it without asking; the others ask first and stop
// when the user cancels.
//
// It calls Windows through the System plugin, not a script: an installer
// that writes a PowerShell script and runs it past the execution policy
// is what antivirus heuristics take for malware.
func nsisCloseApp(c *Config) string {
	running := nsisString(c.windowsName() + " is running. Close it to continue?")
	stuck := nsisString(c.windowsName() + " is still running. Quit it, then try again.")
	var out strings.Builder
	for _, p := range []string{"", "un."} {
		out.WriteString(`; Counts the processes whose executables are in the folder under the
; mode on the stack, not the installer's own: "count" only counts them,
; "close" asks the windows of each to close, as taskkill without /F does,
; and "kill" ends them. QueryFullProcessImageName sees the paths of 64-bit
; processes from the 32-bit installer too.
Function ` + p + `AppProcesses
  Exch $R0
  Exch
  Exch $R1
  Push $R2
  Push $R3
  Push $R4
  Push $R5
  Push $R6
  Push $R7
  Push $R8
  Push $R9
  StrCpy $R3 0
  System::Alloc 65536
  Pop $R2
  IntPtrCmp $R2 0 end
  System::Call 'kernel32::K32EnumProcesses(p R2, i 65536, *i .R4) i .R5'
  IntCmp $R5 0 free
  IntOp $R4 $R4 / 4
  StrCpy $R5 0
  loop:
    IntCmp $R5 $R4 free 0 free
    IntOp $R7 $R5 * 4
    IntPtrOp $R7 $R2 + $R7
    System::Call '*$R7(i .R6)'
    IntOp $R5 $R5 + 1
    System::Call 'kernel32::GetCurrentProcessId() i .R7'
    IntCmp $R6 $R7 loop
    ; PROCESS_QUERY_LIMITED_INFORMATION
    System::Call 'kernel32::OpenProcess(i 0x1000, i 0, i R6) p .R7'
    IntPtrCmp $R7 0 loop
    System::Call 'kernel32::QueryFullProcessImageNameW(p R7, i 0, w .R8, *i ${NSIS_MAX_STRLEN}) i .R9'
    System::Call 'kernel32::CloseHandle(p R7)'
    IntCmp $R9 0 loop
    StrLen $R9 $R1
    StrCpy $R8 $R8 $R9
    ; StrCmp ignores case, as Windows paths do.
    StrCmp $R8 $R1 0 loop
    IntOp $R3 $R3 + 1
    StrCmp $R0 "close" 0 kill
    nsExec::Exec '"$SYSDIR\taskkill.exe" /PID $R6'
    Pop $R9
    Goto loop
    kill:
    StrCmp $R0 "kill" 0 loop
    ; PROCESS_TERMINATE
    System::Call 'kernel32::OpenProcess(i 0x1, i 0, i R6) p .R7'
    IntPtrCmp $R7 0 loop
    System::Call 'kernel32::TerminateProcess(p R7, i 1)'
    System::Call 'kernel32::CloseHandle(p R7)'
    Goto loop
  free:
  System::Free $R2
  end:
  StrCpy $R0 $R3
  Pop $R9
  Pop $R8
  Pop $R7
  Pop $R6
  Pop $R5
  Pop $R4
  Pop $R3
  Pop $R2
  Pop $R1
  Exch $R0
FunctionEnd

; Waits up to 10 s for the processes of the app in the folder on the stack
; to quit, and leaves how many still run.
Function ` + p + `WaitApp
  Exch $R0
  Push $R1
  Push $R2
  StrCpy $R1 100
  loop:
    Push $R0
    Push "count"
    Call ` + p + `AppProcesses
    Pop $R2
    StrCmp $R2 0 done
    IntOp $R1 $R1 - 1
    IntCmp $R1 0 done
    Sleep 100
    Goto loop
  done:
  StrCpy $R0 $R2
  Pop $R2
  Pop $R1
  Exch $R0
FunctionEnd

; Quits the app running from the folder on the stack.
Function ` + p + `CloseApp
  Exch $0
  Push $1
  ; With a backslash, so that the folder is no prefix of another.
  StrCpy $1 $0 1 -1
  StrCmp $1 "\" +2
  StrCpy $0 "$0\"
  Push $0
  Push "count"
  Call ` + p + `AppProcesses
  Pop $1
  StrCmp $1 0 done
  IfSilent close
  MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION ` + running + ` IDOK close
  Abort
  close:
  Push $0
  Push "close"
  Call ` + p + `AppProcesses
  Pop $1
  Push $0
  Call ` + p + `WaitApp
  Pop $1
  StrCmp $1 0 done
  Push $0
  Push "kill"
  Call ` + p + `AppProcesses
  Pop $1
  Push $0
  Call ` + p + `WaitApp
  Pop $1
  StrCmp $1 0 done
  MessageBox MB_OK|MB_ICONSTOP ` + stuck + ` /SD IDOK
  SetErrorLevel 2
  Quit
  done:
  Pop $1
  Pop $0
FunctionEnd

`)
	}
	return out.String()
}

// nsisVersionInfo returns the commands that give the installer the file
// properties of the app, its name, version and publisher, as Explorer
// shows them: an executable without them looks the more like malware.
func nsisVersionInfo(c *Config) string {
	v := windowsVersion(c.Version)
	var b strings.Builder
	fmt.Fprintf(&b, "VIProductVersion \"%d.%d.%d.%d\"\n", v[0], v[1], v[2], v[3])
	key := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "VIAddVersionKey %s %s\n", nsisString(name), nsisString(value))
		}
	}
	key("ProductName", c.windowsName())
	key("FileDescription", c.windowsName()+" Setup")
	key("FileVersion", c.Version)
	key("ProductVersion", c.Version)
	key("CompanyName", c.publisher())
	key("LegalCopyright", c.Copyright)
	return b.String()
}

// uninstallerSigning returns the NSIS command that signs the uninstaller
// of a signed app, and the environment makensis needs to run it. makensis
// writes the uninstaller to a temporary file, runs the command with it and
// puts the signed file into the installer. The command runs mygo, which
// signs the uninstaller as it signs the app (runSignUninstaller); its path
// goes through the environment, since NSIS reads "$" in the script as its
// own syntax.
func uninstallerSigning(c *Config, tool string) (script string, env []string, err error) {
	if !c.Windows.signs() {
		return "", nil, nil
	}
	if major, minor := nsisVersion(tool); major < 3 || major == 3 && minor < 8 {
		warnf("Not signing Uninstall.exe: that needs NSIS 3.08 or later, not %s", tool)
		return "", nil, nil
	}
	self, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	settings, err := json.Marshal(signSettings{Root: c.root, Windows: c.Windows})
	if err != nil {
		return "", nil, err
	}
	signer := `"%` + signerEnv + `%"`
	if runtime.GOOS != "windows" {
		signer = `"$` + signerEnv + `"`
	}
	// "= 0" stops the build when signing fails; NSIS ignores the exit code
	// otherwise.
	return "!uninstfinalize '" + signer + ` sign-uninstaller "%1"' = 0` + "\n",
		[]string{signerEnv + "=" + self, signSettingsEnv + "=" + string(settings)}, nil
}

// nsisVersion returns the version of the NSIS compiler tool, or zeros when
// it does not tell.
func nsisVersion(tool string) (major, minor int) {
	out, err := exec.Command(tool, "-VERSION").Output()
	if err == nil {
		// "v3.13", or "v3.08-3" for a distribution's build.
		fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(string(out)), "v"), "%d.%d", &major, &minor)
	}
	return major, minor
}

// nsisContextMenus are the classes whose right-click menu in Explorer
// windows.contextMenu adds an item to, with the argument each passes the
// app: a file's or a folder's path, or the folder whose background it is.
var nsisContextMenus = []struct{ class, arg string }{
	{`*`, `%1`},
	{`Directory`, `%1`},
	{`Directory\Background`, `%V`},
}

// nsisAssociations returns the installer commands that register, and
// unregister, the file associations, URL schemes and Explorer context menu
// items of the app for the user, opening them with exe.
func nsisAssociations(c *Config, exe string) (register, unregister string) {
	var r, u strings.Builder
	open := `'"$INSTDIR\` + nsisEscape(exe) + `" "%1"'`
	prefix := strings.Map(func(r rune) rune {
		if r < 0x80 && (r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return r
		}
		return -1
	}, c.Identifier)
	for _, fa := range c.FileAssociations {
		for _, ext := range fa.Ext {
			progID := prefix + "." + strings.ToLower(ext)
			class := `Software\Classes\` + progID
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" %s\n", nsisString(class), nsisString(fa.Name))
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" \"$INSTDIR\\%s,0\"\n", nsisString(class+`\DefaultIcon`), nsisEscape(exe))
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" %s\n", nsisString(class+`\shell\open\command`), open)
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s %s \"\"\n", nsisString(`Software\Classes\.`+ext+`\OpenWithProgids`), nsisString(progID))
			fmt.Fprintf(&u, "  DeleteRegKey HKCU %s\n", nsisString(class))
			fmt.Fprintf(&u, "  DeleteRegValue HKCU %s %s\n", nsisString(`Software\Classes\.`+ext+`\OpenWithProgids`), nsisString(progID))
		}
	}
	if label := strings.TrimSpace(c.Windows.ContextMenu); label != "" {
		icon := `"$INSTDIR\` + nsisEscape(exe) + `,0"`
		for _, menu := range nsisContextMenus {
			key := `Software\Classes\` + menu.class + `\shell\` + prefix
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" %s\n", nsisString(key), nsisString(label))
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"Icon\" %s\n", nsisString(key), icon)
			fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" %s\n", nsisString(key+`\command`), `'"$INSTDIR\`+nsisEscape(exe)+`" "`+menu.arg+`"'`)
			fmt.Fprintf(&u, "  DeleteRegKey HKCU %s\n", nsisString(key))
		}
	}
	for _, scheme := range c.URLSchemes {
		key := `Software\Classes\` + scheme
		fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" %s\n", nsisString(key), nsisString("URL:"+c.windowsName()))
		fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"URL Protocol\" \"\"\n", nsisString(key))
		fmt.Fprintf(&r, "  WriteRegStr HKCU %s \"\" %s\n", nsisString(key+`\shell\open\command`), open)
		fmt.Fprintf(&u, "  DeleteRegKey HKCU %s\n", nsisString(key))
	}
	if r.Len() > 0 {
		// Tell Explorer the associations changed.
		notify := "  System::Call 'shell32::SHChangeNotify(i 0x08000000, i 0, p 0, p 0)'\n"
		r.WriteString(notify)
		u.WriteString(notify)
	}
	return r.String(), u.String()
}

// writeNSIS writes the installer's script to path as UTF-8 with a byte order
// mark. The script starts with `Unicode true`, and NSIS reads a script
// without a BOM as ANSI, in the machine's code page, so a name or a path with
// a non-ASCII character, such as 望言字幕导出 or a folder named 中文, would
// not match the files it installs: makensis reports "no files found".
func writeNSIS(path, script string) error {
	b := append([]byte{0xEF, 0xBB, 0xBF}, script...)
	return os.WriteFile(path, b, 0o644)
}

// nsisEscape escapes text for an NSIS string in double quotes.
func nsisEscape(s string) string {
	return strings.NewReplacer(`$`, `$$`, `"`, `$\"`, "\n", `$\n`, "\r", `$\r`, "\t", `$\t`).Replace(s)
}

func nsisString(s string) string { return `"` + nsisEscape(s) + `"` }

// downloadProgress shows how much of a download arrived on t.
type downloadProgress struct {
	t     *task
	n     int64
	total int64 // or -1
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.total > 0 {
		p.t.set(fmt.Sprintf("%s of %s", formatSize(p.n), formatSize(p.total)))
	} else {
		p.t.set(formatSize(p.n))
	}
	return len(b), nil
}
