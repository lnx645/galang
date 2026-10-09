; galang.iss — GaLang Windows installer (Inno Setup 6).
;
; Used by CI (the "Platform Installers" workflow):
;   ISCC /DMyAppVersion=<version> galang.iss
; with gar.exe (the release binary, extracted from
; gar-windows-amd64.zip) placed next to this script.
; Output: out/GaLang-Setup-<version>.exe.
;
; The x64compatible architecture set covers Windows x64 AND Windows on
; ARM (the amd64 binary runs through Windows x64 emulation — consistent
; with the docs because the native extension variant windows-arm64 does
; not exist yet).

#define MyAppName "GaLang"
#ifndef MyAppVersion
  #define MyAppVersion "0.0.0-dev"
#endif
#define MyAppPublisher "GaLang Project"
#define MyAppURL "https://github.com/lnx645/galang"
#define MyAppExeName "gar.exe"

[Setup]
AppId={{8D2A0B7E-6F1C-4C41-9A2E-3B5E6C7D8091}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}/issues
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={autopf}\{#MyAppName}
DisableProgramGroupPage=yes
OutputDir=out
OutputBaseFilename=GaLang-Setup-{#MyAppVersion}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
ChangesEnvironment=yes
UninstallDisplayName={#MyAppName} {#MyAppVersion}
UninstallDisplayIcon={app}\{#MyAppExeName}

[Files]
Source: "gar.exe"; DestDir: "{app}"; Flags: ignoreversion

[Registry]
; Add {app} to the machine PATH when it is not there yet; Inno keeps
; the previous value but does NOT restore it on uninstall (see the
; CurUninstallStepChanged handler below).
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
  ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
  Check: NeedsAddPath(ExpandConstant('{app}'))

[Code]
const
  EnvPathKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';

{ Check whether the PATH already contains the entry (case-insensitive,
  semicolon-separated). }
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKLM, EnvPathKey, 'Path', OrigPath) then
  begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Uppercase(Param) + ';',
                ';' + Uppercase(OrigPath) + ';') = 0;
end;

{ On uninstall, remove the install directory from the machine PATH.
  Inno does NOT restore registry values it modified, so without this
  step the PATH keeps a dead entry — proven by the silent test in CI.
  (Avoid curly braces inside Pascal comments: comments do not nest, so
  a stray brace closes them early.) }
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  Path, NewPath, Token, Rest, App: string;
  P: Integer;
begin
  if CurUninstallStep <> usUninstall then
    exit;
  if not RegQueryStringValue(HKLM, EnvPathKey, 'Path', Path) then
    exit;
  App := Uppercase(ExpandConstant('{app}'));
  NewPath := '';
  Rest := Path;
  while Rest <> '' do
  begin
    P := Pos(';', Rest);
    if P = 0 then
    begin
      Token := Rest;
      Rest := '';
    end
    else
    begin
      Token := Copy(Rest, 1, P - 1);
      Rest := Copy(Rest, P + 1, Length(Rest));
    end;
    if (Token <> '') and (Uppercase(Token) <> App) then
    begin
      if NewPath <> '' then
        NewPath := NewPath + ';';
      NewPath := NewPath + Token;
    end;
  end;
  { Never write an empty PATH. }
  if NewPath <> '' then
    RegWriteStringValue(HKLM, EnvPathKey, 'Path', NewPath);
end;
