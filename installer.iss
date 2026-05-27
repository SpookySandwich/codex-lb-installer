[Setup]
AppName=CodexLB
AppVersion=1.19.0-beta.1
AppVerName=CodexLB 1.19.0-beta.1
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
OutputBaseFilename=CodexLB_Installer_1.19.0_beta.1
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

[Code]
var
  DeleteDataPage: TWizardPage;
  DeleteDataCheckBox: TNewCheckBox;

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
