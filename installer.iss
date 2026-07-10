[Setup]
AppName=CodexLB
AppVersion=1.20.2-beta.1
AppVerName=CodexLB 1.20.2-beta.1
SetupIconFile=codex_lb_icon.ico
AppPublisher=Soju06
AppPublisherURL=https://github.com/Soju06/codex-lb
AppSupportURL=https://github.com/Soju06/codex-lb/issues
DefaultDirName={localappdata}\Programs\CodexLB
DefaultGroupName=CodexLB
UninstallDisplayIcon={app}\launcher.exe
Compression=lzma2
SolidCompression=yes
OutputDir=dist
OutputBaseFilename=CodexLB_Installer_1.20.2_beta.1
DisableProgramGroupPage=yes
DisableDirPage=no
PrivilegesRequired=lowest
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
CloseApplications=no
RestartApplications=no

[Files]
Source: "dist\bundle\*"; DestDir: "{app}"; Flags: recursesubdirs createallsubdirs

[Icons]
Name: "{group}\CodexLB"; Filename: "{app}\launcher.exe"; IconFilename: "{app}\launcher.exe"; IconIndex: 0
Name: "{group}\Uninstall CodexLB"; Filename: "{uninstallexe}"
Name: "{userdesktop}\CodexLB"; Filename: "{app}\launcher.exe"; IconFilename: "{app}\launcher.exe"; IconIndex: 0

[Run]
Filename: "{app}\launcher.exe"; Description: "Launch CodexLB"; Flags: nowait postinstall skipifsilent
Filename: "{app}\launcher.exe"; Flags: nowait skipifnotsilent

[Code]
var
  DeleteDataPage: TWizardPage;
  DeleteDataCheckBox: TNewCheckBox;

function PowerShellString(const Value: String): String;
var
  Escaped: String;
begin
  Escaped := Value;
  StringChangeEx(Escaped, '''', '''''', True);
  Result := '''' + Escaped + '''';
end;

procedure KillProcessByPath(const ProcessPath: String);
var
  ResultCode: Integer;
  ScriptPath: String;
  Script: String;
begin
  ScriptPath := ExpandConstant('{tmp}\CodexLB_StopProcess.ps1');
  Script :=
    '$target = ' + PowerShellString(ProcessPath) + #13#10 +
    'Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath -ieq $target } | ' +
    'ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }' + #13#10;

  if SaveStringToFile(ScriptPath, Script, False) then
  begin
    Exec(
      ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
      '-NoProfile -ExecutionPolicy Bypass -File "' + ScriptPath + '"',
      '',
      SW_HIDE,
      ewWaitUntilTerminated,
      ResultCode
    );
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  KillProcessByPath(ExpandConstant('{app}\launcher.exe'));
  KillProcessByPath(ExpandConstant('{app}\python\python.exe'));
  Sleep(2000);
  Result := '';
end;

procedure InitializeUninstallProgressForm();
begin
  DeleteDataPage := CreateCustomPage(wpWelcome, 'Uninstall Options', 'Would you like to remove user data as well?');
  DeleteDataCheckBox := TNewCheckBox.Create(DeleteDataPage);
  DeleteDataCheckBox.Caption := 'Delete my configuration, databases, and API keys (~/.codex-lb)';
  DeleteDataCheckBox.State := cbUnchecked;
  DeleteDataCheckBox.Parent := DeleteDataPage.Surface;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    if DeleteDataCheckBox.Checked then
    begin
      DataDir := ExpandConstant('{userprofile}\.codex-lb');
      if DirExists(DataDir) then
      begin
        DelTree(DataDir, True, True, True);
      end;
    end;
  end;
end;
