#ifndef CodexLBVersion
  #define CodexLBVersion "1.20.2-beta.1"
#endif

#ifndef CodexLBOutputBaseFilename
  #define CodexLBOutputBaseFilename "CodexLB_Installer_1.20.2_beta.1"
#endif

#ifndef CodexLBPayloadId
  #define CodexLBPayloadId "legacy"
#endif

[Setup]
AppName=CodexLB
AppId=CodexLB
AppVersion={#CodexLBVersion}
AppVerName=CodexLB {#CodexLBVersion}
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
OutputBaseFilename={#CodexLBOutputBaseFilename}
DisableProgramGroupPage=yes
DisableDirPage=no
PrivilegesRequired=lowest
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64
CloseApplications=no
RestartApplications=no
SetupMutex=CodexLBSetupMutex

[Files]
Source: "dist\bundle\launcher.exe"; DestDir: "{app}"
Source: "dist\bundle\python\*"; DestDir: "{app}\versions\{#CodexLBPayloadId}\python"; Flags: recursesubdirs createallsubdirs

[Icons]
Name: "{group}\CodexLB"; Filename: "{app}\launcher.exe"; IconFilename: "{app}\launcher.exe"; IconIndex: 0
Name: "{group}\Uninstall CodexLB"; Filename: "{uninstallexe}"
Name: "{userdesktop}\CodexLB"; Filename: "{app}\launcher.exe"; IconFilename: "{app}\launcher.exe"; IconIndex: 0

[Run]
Filename: "{app}\launcher.exe"; Flags: nowait runasoriginaluser; Check: ShouldRestartAfterInstall
Filename: "{app}\launcher.exe"; Description: "Launch CodexLB"; Flags: nowait postinstall skipifsilent runasoriginaluser; Check: ShouldUseFreshInstallLaunch
Filename: "{app}\launcher.exe"; Flags: nowait skipifnotsilent runasoriginaluser; Check: ShouldUseFreshInstallLaunch

[Code]
var
  DeleteDataPage: TWizardPage;
  DeleteDataCheckBox: TNewCheckBox;
  UpdateRequested: Boolean;
  StoppedExistingApp: Boolean;
  SetupCompleted: Boolean;
  RecoveryAttempted: Boolean;

const
  StopResultNoneRunning = 0;
  StopResultStoppedExisting = 10;
  StopResultCouldNotStop = 21;
  StopResultQueryFailed = 22;

function HasCommandLineParameter(const Parameter: String): Boolean;
var
  Index: Integer;
begin
  Result := False;
  for Index := 1 to ParamCount do
  begin
    if CompareText(ParamStr(Index), Parameter) = 0 then
    begin
      Result := True;
      Exit;
    end;
  end;
end;

function InitializeSetup(): Boolean;
begin
  UpdateRequested := HasCommandLineParameter('/CODEXLBUPDATE');
  StoppedExistingApp := False;
  SetupCompleted := False;
  RecoveryAttempted := False;
  if UpdateRequested then
    Log('CodexLB update handoff requested; running application will remain active until PrepareToInstall.');
  Result := True;
end;

function ShouldRestartAfterInstall(): Boolean;
begin
  Result := UpdateRequested or StoppedExistingApp;
end;

function ShouldUseFreshInstallLaunch(): Boolean;
begin
  Result := not ShouldRestartAfterInstall();
end;

function PowerShellString(const Value: String): String;
var
  Escaped: String;
begin
  Escaped := Value;
  StringChangeEx(Escaped, '''', '''''', True);
  Result := '''' + Escaped + '''';
end;

function StopInstalledApplication(var StoppedExisting: Boolean): String;
var
  ResultCode: Integer;
  ScriptPath: String;
  MarkerPath: String;
  Script: String;
  PowerShellPath: String;
begin
  Result := '';
  StoppedExisting := False;
  ScriptPath := ExpandConstant('{tmp}\CodexLB_StopProcess.ps1');
  MarkerPath := ExpandConstant('{tmp}\CodexLB_WasRunning.marker');
  PowerShellPath := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  DeleteFile(MarkerPath);
  Script :=
    '$ErrorActionPreference = ''Stop''' + #13#10 +
    '$launcherPath = ' + PowerShellString(ExpandConstant('{app}\launcher.exe')) + #13#10 +
    '$legacyPythonPath = ' + PowerShellString(ExpandConstant('{app}\python\python.exe')) + #13#10 +
    '$versionsRoot = ' + PowerShellString(ExpandConstant('{app}\versions')) + #13#10 +
    '$ownedPythonPattern = ''^'' + [Regex]::Escape($versionsRoot) + ' +
      '''\\(?:legacy|[0-9a-fA-F]{64})\\python\\python[.]exe$''' + #13#10 +
    'function Get-CodexLBProcesses {' + #13#10 +
    '  @(Get-CimInstance Win32_Process -ErrorAction Stop | Where-Object { ' +
      '$path = $_.ExecutablePath; $path -and (($path -ieq $launcherPath) -or ' +
      '($path -ieq $legacyPythonPath) -or ($path -imatch $ownedPythonPattern)) })' + #13#10 +
    '}' + #13#10 +
    'try { $processes = @(Get-CodexLBProcesses) } catch { exit ' +
      IntToStr(StopResultQueryFailed) + ' }' + #13#10 +
    'if ($processes.Count -eq 0) { exit ' + IntToStr(StopResultNoneRunning) + ' }' + #13#10 +
    '$stopFailed = $false' + #13#10 +
    '$stoppedAny = $false' + #13#10 +
    'foreach ($process in $processes) {' + #13#10 +
    '  try { Stop-Process -Id $process.ProcessId -Force -ErrorAction Stop; $stoppedAny = $true } ' +
      'catch { $stopFailed = $true }' + #13#10 +
    '}' + #13#10 +
    'if ($stoppedAny) { Set-Content -LiteralPath ' + PowerShellString(MarkerPath) +
      ' -Value ''stopped'' -Encoding ASCII -ErrorAction Stop }' + #13#10 +
    '$deadline = [DateTime]::UtcNow.AddSeconds(10)' + #13#10 +
    'while ([DateTime]::UtcNow -lt $deadline) {' + #13#10 +
    '  try { $remaining = @(Get-CodexLBProcesses) } catch { exit ' +
      IntToStr(StopResultQueryFailed) + ' }' + #13#10 +
    '  if ($remaining.Count -eq 0) { exit ' + IntToStr(StopResultStoppedExisting) + ' }' + #13#10 +
    '  Start-Sleep -Milliseconds 100' + #13#10 +
    '}' + #13#10 +
    'exit ' + IntToStr(StopResultCouldNotStop) + #13#10;

  if not SaveStringToFile(ScriptPath, Script, False) then
  begin
    DeleteFile(MarkerPath);
    Result := 'Setup could not create its CodexLB process-stop helper.';
    Exit;
  end;

  if not FileExists(PowerShellPath) then
  begin
    DeleteFile(ScriptPath);
    DeleteFile(MarkerPath);
    Result := 'Windows PowerShell was not found. CodexLB was not stopped, so the update cannot continue safely.';
    Exit;
  end;

  if not Exec(
    PowerShellPath,
    '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' + ScriptPath + '"',
    '',
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode
  ) then
  begin
    DeleteFile(ScriptPath);
    DeleteFile(MarkerPath);
    Result := 'Setup could not start Windows PowerShell to stop CodexLB. The update has been aborted.';
    Exit;
  end;
  DeleteFile(ScriptPath);
  StoppedExisting := FileExists(MarkerPath);
  DeleteFile(MarkerPath);

  case ResultCode of
    StopResultNoneRunning:
      Log('No CodexLB process was running from the selected installation directory.');
    StopResultStoppedExisting:
      begin
        Log('Stopped the existing CodexLB processes and verified that they exited.');
      end;
    StopResultCouldNotStop:
      Result := 'CodexLB did not stop within 10 seconds. No files were installed; close CodexLB and try again.';
    StopResultQueryFailed:
      Result := 'Setup could not verify the running CodexLB processes. No files were installed; close CodexLB and try again.';
  else
    Result := 'The CodexLB process-stop helper failed with exit code ' +
      IntToStr(ResultCode) + '. No files were installed.';
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  StoppedNow: Boolean;
begin
  Result := StopInstalledApplication(StoppedNow);
  if StoppedNow then
    StoppedExistingApp := True;
end;

procedure PruneOldPayloads();
var
  ResultCode: Integer;
  ScriptPath: String;
  Script: String;
  PowerShellPath: String;
begin
  ScriptPath := ExpandConstant('{tmp}\CodexLB_PrunePayloads.ps1');
  PowerShellPath := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  Script :=
    '$ErrorActionPreference = ''Stop''' + #13#10 +
    '$versionsRoot = ' + PowerShellString(ExpandConstant('{app}\versions')) + #13#10 +
    '$current = ' + PowerShellString('{#CodexLBPayloadId}') + #13#10 +
    '$ownedIdPattern = ''^(?:legacy|[0-9a-fA-F]{64})$''' + #13#10 +
    'if ($current -notmatch $ownedIdPattern) { exit 2 }' + #13#10 +
    'if (-not [IO.Directory]::Exists($versionsRoot)) { exit 0 }' + #13#10 +
    '$rootFull = [IO.Path]::GetFullPath($versionsRoot)' + #13#10 +
    '$previous = @(Get-ChildItem -LiteralPath $rootFull -Directory -Force -ErrorAction Stop | ' +
      'Where-Object { $_.Name -match $ownedIdPattern -and $_.Name -ine $current -and ' +
      '-not ($_.Attributes -band [IO.FileAttributes]::ReparsePoint) } | ' +
      'Sort-Object LastWriteTimeUtc -Descending)' + #13#10 +
    '$failed = $false' + #13#10 +
    'foreach ($entry in @($previous | Select-Object -Skip 1)) {' + #13#10 +
    '  $candidate = [IO.Path]::GetFullPath($entry.FullName)' + #13#10 +
    '  if (([IO.Path]::GetDirectoryName($candidate) -ine $rootFull) -or ' +
      '($entry.Name -notmatch $ownedIdPattern)) { $failed = $true; continue }' + #13#10 +
    '  try { Remove-Item -LiteralPath $candidate -Recurse -Force -ErrorAction Stop } ' +
      'catch { $failed = $true }' + #13#10 +
    '}' + #13#10 +
    'if ($failed) { exit 1 }' + #13#10 +
    'exit 0' + #13#10;

  if not SaveStringToFile(ScriptPath, Script, False) then
  begin
    Log('Could not create the old-payload cleanup helper; continuing without pruning.');
    Exit;
  end;
  if not FileExists(PowerShellPath) then
  begin
    DeleteFile(ScriptPath);
    Log('Windows PowerShell was not found; continuing without pruning old payloads.');
    Exit;
  end;
  if not Exec(
    PowerShellPath,
    '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' + ScriptPath + '"',
    '',
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode
  ) then
  begin
    DeleteFile(ScriptPath);
    Log('Could not start the old-payload cleanup helper; continuing without pruning.');
    Exit;
  end;
  DeleteFile(ScriptPath);
  if ResultCode = 0 then
    Log('Pruned old owned payload versions, retaining the current and newest previous payload.')
  else
    Log('Old-payload cleanup was incomplete (exit code ' + IntToStr(ResultCode) + '); continuing.');
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    PruneOldPayloads();
    SetupCompleted := True;
    Log('CodexLB installation completed; the installed launcher will be started by the [Run] entry.');
  end;
end;

procedure RelaunchAfterFailedSetup();
var
  LauncherPath: String;
  ResultCode: Integer;
begin
  if RecoveryAttempted then
    Exit;
  RecoveryAttempted := True;
  LauncherPath := ExpandConstant('{app}\launcher.exe');

  if not FileExists(LauncherPath) then
  begin
    Log('Recovery launch skipped because launcher.exe is not present after Setup rollback.');
    Exit;
  end;

  if Exec(LauncherPath, '', ExpandConstant('{app}'), SW_SHOWNORMAL, ewNoWait, ResultCode) then
    Log('Relaunched CodexLB after the update did not complete.')
  else
    Log('Failed to relaunch CodexLB after the update did not complete: ' +
      SysErrorMessage(ResultCode));
end;

procedure DeinitializeSetup();
begin
  if StoppedExistingApp and (not SetupCompleted) then
    RelaunchAfterFailedSetup();
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
