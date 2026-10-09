; Humanizer Windows 安装包(Inno Setup 6)。
; CI 里这样编译(在 app/ 目录下):
;   iscc /DAppVersion=0.3.2 packaging\windows\humanizer.iss
; 输入:dist\win\ 下已经放好 Humanizer.exe 和 engine\{cuda,vulkan,cpu}\
; 输出:dist\Humanizer-<版本>-windows-x64-setup.exe
;
; 按用户安装(不需要管理员权限),装到 %LOCALAPPDATA%\Programs\Humanizer;
; 模型和日志默认在 %LOCALAPPDATA%\Humanizer(启动器管),可以在网页上的「存储位置」里改。
; 卸载时读 location.json,问要不要连改过的那个目录一起删。

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
// 从 location.json 里读出一个字符串字段的值。
// 只处理"key": "value" 这种形态(启动器写出来的就是它),解析不了就返回空串,
// 调用方按默认目录处理。
function ReadLocationField(const Key: String): String;
var
  Content, P, Colon, Q1, Q2, Value: String;
begin
  Result := '';
  if not LoadStringFromFile(ExpandConstant('{localappdata}\Humanizer\location.json'), Content) then
    Exit;
  P := Pos('"' + Key + '"', Content);
  if P = 0 then Exit;
  Colon := PosEx(':', Content, P);
  if Colon = 0 then Exit;
  Q1 := PosEx('"', Content, Colon);            // 值的左引号
  if Q1 = 0 then Exit;
  Q2 := PosEx('"', Content, Q1 + 1);          // 值的右引号
  if Q2 = 0 then Exit;
  Value := Copy(Content, Q1 + 1, Q2 - Q1 - 1);
  // JSON 里反斜杠是转义的,还原回来(Pascal 字面量 '\\' 就是两个反斜杠)
  StringChange(Value, '\\', '\');
  Result := Value;
end;

// 盘根(C:\、D:\ 这类)一律不删:数据目录是用户自己填的路径,
// 万一填成了盘根,卸载不能跟着把整盘删掉。
function IsDeletableDir(const Dir: String): Boolean;
var
  S: String;
begin
  S := RemoveBackslash(Dir);
  Result := (Pos(':', S) = 0) or (Length(S) > 3);
end;

// 卸载时要清理的目录:优先用户在网页上改过的运行目录,其次模型目录,最后默认目录。
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir, ModelDir: String;
  Dirs: array[0..1] of String;
  I, N: Integer;
begin
  if CurUninstallStep <> usPostUninstall then Exit;

  DataDir := ReadLocationField('data_dir');
  ModelDir := ReadLocationField('model_dir');
  if DataDir = '' then
    DataDir := ExpandConstant('{localappdata}\Humanizer');

  N := 0;
  if DirExists(DataDir) and IsDeletableDir(DataDir) then begin Dirs[N] := DataDir; Inc(N); end;
  // 模型目录单独放在外面(没设过就等于数据目录下的 models,那时不用再问一遍)
  if (ModelDir <> '') and (ModelDir <> DataDir) and DirExists(ModelDir) and IsDeletableDir(ModelDir) then begin Dirs[N] := ModelDir; Inc(N); end;

  for I := 0 to N - 1 do
    if not UninstallSilent then
      if MsgBox(FmtMessage(CustomMessage('AskDeleteModels'), [Dirs[I]]), mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        DelTree(Dirs[I], True, True, True);
end;
