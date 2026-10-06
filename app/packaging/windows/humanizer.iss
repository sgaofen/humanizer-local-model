; Humanizer Windows 安装包(Inno Setup 6)。
; CI 里这样编译(在 app/ 目录下):
;   iscc /DAppVersion=0.3.0 packaging\windows\humanizer.iss
; 输入:dist\win\ 下已经放好 Humanizer.exe 和 engine\{cuda,vulkan,cpu}\
; 输出:dist\Humanizer-<版本>-windows-x64-setup.exe
;
; 按用户安装(不需要管理员权限),装到 %LOCALAPPDATA%\Programs\Humanizer;
; 模型和日志在 %LOCALAPPDATA%\Humanizer(启动器管),卸载时问要不要一起删。

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

[Setup]
AppId={{5B7D3E4A-9C2F-4E61-A8D3-2F6B1C0E9A47}
AppName=Humanizer
AppVersion={#AppVersion}
AppVerName=Humanizer {#AppVersion}
AppPublisher=humanizer
AppPublisherURL=https://github.com/sgaofen/humanizer
DefaultDirName={localappdata}\Programs\Humanizer
DisableDirPage=auto
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
OutputDir=..\..\dist
OutputBaseFilename=Humanizer-{#AppVersion}-windows-x64-setup
SetupIconFile=..\icon\icon.ico
UninstallDisplayIcon={app}\Humanizer.exe
UninstallDisplayName=Humanizer
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; 升级/卸载时如果 Humanizer 正在跑,用 Restart Manager 关掉(引擎挂在它的 Job 上会一起退出)
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "en"; MessagesFile: "compiler:Default.isl"
; 简体中文语言文件不是 Inno Setup 自带的官方翻译,CI 会先下载到 compiler 目录;没有就只出英文界面
#if FileExists(AddBackslash(CompilerPath) + "Languages\ChineseSimplified.isl")
Name: "zh"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"
#endif

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "..\..\dist\win\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\Humanizer"; Filename: "{app}\Humanizer.exe"
Name: "{autodesktop}\Humanizer"; Filename: "{app}\Humanizer.exe"; Tasks: desktopicon

[Run]
Filename: "{app}\Humanizer.exe"; Description: "{cm:LaunchProgram,Humanizer}"; Flags: nowait postinstall skipifsilent

[UninstallRun]
; 只关 Humanizer.exe;不要按名字杀 llama-server.exe(可能误杀用户自己跑的)
Filename: "{sys}\taskkill.exe"; Parameters: "/f /im Humanizer.exe"; Flags: runhidden; RunOnceId: "StopHumanizer"

[CustomMessages]
en.AskDeleteModels=Also delete the downloaded models and logs?%n%n%1%n%nThe model files are large (several GB). Choose No to keep them for a later reinstall.
zh.AskDeleteModels=要不要把已下载的模型和日志也一起删掉？%n%n%1%n%n模型文件很大（几个 GB）。选「否」可以留着，下次重装直接用。

[Code]
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{localappdata}\Humanizer');
    if DirExists(DataDir) and not UninstallSilent then
      if MsgBox(FmtMessage(CustomMessage('AskDeleteModels'), [DataDir]), mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        DelTree(DataDir, True, True, True);
  end;
end;
