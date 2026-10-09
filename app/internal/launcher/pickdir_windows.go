package launcher

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// pickDir 弹 Windows 的「选择文件夹」窗口。
//
// 首选 IFileOpenDialog(FOS_PICKFOLDERS)—— 就是资源管理器同款的新对话框,
// 并且把 owner 设成"用户点按钮那一刻的前台窗口"(一般是浏览器),这样它弹出来
// 压在那个窗口上面、能抢回焦点;不带 owner 时它会落在所有窗口后面。
// (WinForms FolderBrowserDialog 在无控制台的子进程里会退化成 XP 树形界面,只作退路。)
// 新对话框失败退 WinForms,再失败退 Shell.Application。
// 起始目录和标题走环境变量传,不拼进脚本,免得路径里的引号出问题。
// 用户取消时返回空字符串,不报错。
func pickDir(ctx context.Context, from, title string) (string, error) {
	if from == "" {
		from = "."
	}
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Sta", "-ExecutionPolicy", "Bypass",
		"-Command", pickScript)
	cmd.Env = append(os.Environ(), "HZ_PICK_START="+from, "HZ_PICK_TITLE="+title)
	prepareCmd(cmd) // 不弹控制台窗口;对话框自己的窗口照常显示
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", ctx.Err()
	case err != nil:
		// 三种方式都不行:细节进日志,给网页一句能看懂的话
		msg := strings.TrimSpace(firstLine(errb.String()))
		if msg == "" {
			msg = "三种 Windows 文件夹选择器都没能启动"
		}
		return "", errors.New(msg)
	}
	return absDir(strings.TrimSpace(out.String())), nil
}

// 成功把选中的路径打到 stdout(取消则不打);哪一步失败,那一步的原因写 stderr,退出码非 0。
const pickScript = `
$ErrorActionPreference = 'Stop'
$modern = @'
using System;
using System.Runtime.InteropServices;
public static class FolderPicker
{
    [DllImport("user32.dll")] static extern IntPtr GetForegroundWindow();
    [DllImport("user32.dll")] static extern bool IsWindow(IntPtr hWnd);
    [DllImport("user32.dll")] static extern uint GetWindowThreadProcessId(IntPtr hWnd, out uint pid);
    [ComImport, Guid("DC1C5A9C-E88A-4DDE-A5A1-60F82A20AEF7")]
    class FileOpenDialogRCW { }
    [ComImport, Guid("42f85136-db7e-439c-85f1-e4075d135fc8"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IFileDialog
    {
        [PreserveSig] int Show(IntPtr hwndOwner);            // IModalWindow
        void SetFileTypes(uint cFileTypes, IntPtr rgFilterSpec);
        void SetFileTypeIndex(uint iFileType);
        void GetFileTypeIndex(out uint piFileType);
        void Advise(IntPtr pfde, out uint pdwCookie);
        void Unadvise(uint dwCookie);
        void SetOptions(uint fos);
        void GetOptions(out uint pfos);
        void SetDefaultFolder(IShellItem psi);
        void SetFolder(IShellItem psi);
        void GetFolder(out IShellItem ppsi);
        void GetCurrentSelection(out IShellItem ppsi);
        void SetFileName([MarshalAs(UnmanagedType.LPWStr)] string pszName);
        void GetFileName([MarshalAs(UnmanagedType.LPWStr)] out string pszName);
        void SetTitle([MarshalAs(UnmanagedType.LPWStr)] string pszTitle);
        void SetOkButtonLabel([MarshalAs(UnmanagedType.LPWStr)] string pszText);
        void SetFileNameLabel([MarshalAs(UnmanagedType.LPWStr)] string pszLabel);
        void GetResult(out IShellItem ppsi);
        void AddPlace(IShellItem psi, int fdap);
        void SetDefaultExtension([MarshalAs(UnmanagedType.LPWStr)] string pszDefaultExtension);
        void Close(int hr);
        void SetClientGuid(ref Guid guid);
        void ClearClientData();
        void SetFilter(IntPtr pFilter);
        void GetResults(out IntPtr ppenum);
        void GetItemType(out int pitm);
    }
    [ComImport, Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IShellItem
    {
        void BindToHandler(IntPtr pbc, ref Guid bhid, ref Guid riid, out IntPtr ppvOut);
        void GetParent(out IShellItem ppsi);
        void GetDisplayName(int sigdnName, [MarshalAs(UnmanagedType.LPWStr)] out string ppszDisplayName);
        void GetAttributes(uint sfgaoMask, out uint psfgaoAttribs);
        void Compare(IShellItem psi, uint hint, out int piOrder);
    }
    [DllImport("shell32.dll", CharSet = CharSet.Unicode)]
    static extern int SHCreateItemFromParsingName([MarshalAs(UnmanagedType.LPWStr)] string pszPath, IntPtr pbc, ref Guid riid, out IShellItem ppv);
    public static string Select(string startPath, string title)
    {
        IFileDialog dlg = (IFileDialog)new FileOpenDialogRCW();
        dlg.SetOptions(0x20 | 0x40);           // FOS_PICKFOLDERS | FOS_FORCEFILESYSTEM
        if (!string.IsNullOrEmpty(title)) dlg.SetTitle(title);
        if (!string.IsNullOrEmpty(startPath) && System.IO.Directory.Exists(startPath))
        {
            IShellItem start = null;
            Guid iid = new Guid("43826d1e-e718-42ee-bc55-a1e261c37bfe");
            if (SHCreateItemFromParsingName(startPath, IntPtr.Zero, ref iid, out start) == 0) dlg.SetFolder(start);
        }
        // owner = 用户点按钮那一刻的前台窗口(一般是浏览器):弹窗压在上面、能抢回焦点。
        // 没有有效前台窗口(自动化会话等)就用无 owner,Show 偶尔因 owner 出问题也退无 owner,
        // 保证任何环境里都是新式对话框。
        IntPtr owner = GetForegroundWindow();
        if (owner != IntPtr.Zero && !IsWindow(owner)) owner = IntPtr.Zero;
        int hr;
        try { hr = dlg.Show(owner); }
        catch (Exception) { if (owner == IntPtr.Zero) throw; hr = dlg.Show(IntPtr.Zero); }
        if (hr == unchecked((int)0x800704C7)) return null;   // 用户取消
        if (hr != 0) Marshal.ThrowExceptionForHR(hr);
        IShellItem item = null; dlg.GetResult(out item);
        string path = null; item.GetDisplayName(unchecked((int)0x80028000), out path);   // SIGDN_FILESYSPATH
        return path;
    }
}
'@
try {
    Add-Type -TypeDefinition $modern
    $p = [FolderPicker]::Select($env:HZ_PICK_START, $env:HZ_PICK_TITLE)
    if ($p) { [Console]::Out.Write($p) }
    exit 0
} catch { [Console]::Error.WriteLine('现代选择器: ' + $_.Exception.Message) }
try {
    Add-Type -AssemblyName System.Windows.Forms
    $d = New-Object System.Windows.Forms.FolderBrowserDialog
    $d.Description = $env:HZ_PICK_TITLE
    $d.ShowNewFolderButton = $true
    if ($env:HZ_PICK_START -and (Test-Path -LiteralPath $env:HZ_PICK_START)) { $d.SelectedPath = $env:HZ_PICK_START }
    if ($d.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write($d.SelectedPath) }
    exit 0
} catch { [Console]::Error.WriteLine('WinForms 选择器: ' + $_.Exception.Message) }
try {
    $sh = New-Object -ComObject Shell.Application
    $r = $sh.BrowseForFolder(0, $env:HZ_PICK_TITLE, 0x51, $env:HZ_PICK_START)
    if ($r) { [Console]::Out.Write($r.Self.Path) }
    exit 0
} catch { [Console]::Error.WriteLine('Shell 选择器: ' + $_.Exception.Message) }
exit 1
`

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
