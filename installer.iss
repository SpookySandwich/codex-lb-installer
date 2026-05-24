[Setup]
AppName=Codex Load Balancer
AppVersion=1.19.0-beta.1
AppPublisher=Soju06
AppPublisherURL=https://github.com/Soju06/codex-lb
AppSupportURL=https://github.com/Soju06/codex-lb/issues
DefaultDirName={localappdata}\Programs\CodexLB
DefaultGroupName=Codex Load Balancer
UninstallDisplayIcon={app}\launcher.exe
Compression=lzma2
SolidCompression=yes
OutputDir=dist
OutputBaseFilename=CodexLB_Installer
DisableProgramGroupPage=yes
DisableDirPage=no
PrivilegesRequired=lowest
ArchitecturesAllowed=x64
ArchitecturesInstallIn64BitMode=x64

[Files]
Source: "dist\bundle\*"; DestDir: "{app}"; Flags: recursesubdirs createallsubdirs

[Icons]
Name: "{group}\Codex Load Balancer"; Filename: "{app}\launcher.exe"
Name: "{group}\Uninstall Codex Load Balancer"; Filename: "{uninstallexe}"
Name: "{userdesktop}\Codex Load Balancer"; Filename: "{app}\launcher.exe"

[Run]
Filename: "{app}\launcher.exe"; Description: "Launch Codex Load Balancer"; Flags: nowait postinstall skipifsilent

[Code]
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    if MsgBox('Do you want to delete your Codex Load Balancer configuration, databases, and API keys stored in your user profile folder (~/.codex-lb)?', mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
    begin
      DataDir := ExpandConstant('{userprofile}\.codex-lb');
      if DirExists(DataDir) then
      begin
        DelTree(DataDir, True, True, True);
      end;
    end;
  end;
end;
