; garurda.iss — pemasang Windows Garurda (Inno Setup 6).
;
; Dipakai CI (workflow "Pemasang Platform"):
;   ISCC /DMyAppVersion=<versi> garurda.iss
; dengan gar.exe (binari rilis, hasil ekstrak gar-windows-amd64.zip)
; seberkas dengannya. Hasil: out/Garurda-Setup-<versi>.exe.
;
; Arsitektur x64compatible = Windows x64 ATAU Windows on ARM (binari
; amd64 dijalankan lewat emulasi x64 Windows — konsisten dengan panduan
; docs karena varian ekstensi native belum punya windows-arm64).

#define MyAppName "Garurda"
#ifndef MyAppVersion
  #define MyAppVersion "0.0.0-dev"
#endif
#define MyAppPublisher "Proyek Garurda"
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
OutputBaseFilename=Garurda-Setup-{#MyAppVersion}
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
; Tambahkan {app} ke PATH mesin bila belum ada; Inno menyimpan nilai
; lama dan memulihkannya saat uninstall.
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
  ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
  Check: NeedsAddPath(ExpandConstant('{app}'))

[Code]
{ Cek keberadaan entri PATH (case-insensitive, dipisah titik koma). }
function NeedsAddPath(Param: string): Boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKLM,
    'SYSTEM\CurrentControlSet\Control\Session Manager\Environment',
    'Path', OrigPath) then
  begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Uppercase(Param) + ';',
                ';' + Uppercase(OrigPath) + ';') = 0;
end;
