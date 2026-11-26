#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Instala o blockd no Windows: binários, grupo local, serviço.

.DESCRIPTION
    Rode num PowerShell como administrador, na pasta com blockd.exe e
    blockctl.exe (a saída de `make windows`). Rodar de novo atualiza.

.PARAMETER User
    Usuário que vai usar o blockctl e a GUI sem ser administrador. Entra no
    grupo local "blockd", que tem acesso ao socket do daemon.

.PARAMETER Version
    Versão mostrada em Configurações > Aplicativos (o blockd-setup.exe passa).
#>
param(
    [string]$User = $env:USERNAME,
    [string]$Source = $PSScriptRoot,
    [string]$Version = ''
)
$ErrorActionPreference = 'Stop'

$dest    = Join-Path $env:ProgramFiles 'blockd'
$state   = Join-Path $env:ProgramData 'blockd'
$svcName = 'blockd'

foreach ($b in 'blockd.exe', 'blockctl.exe') {
    if (-not (Test-Path (Join-Path $Source $b))) {
        throw "não achei $b em $Source (rode 'make windows' antes)"
    }
}

# 1. Para o serviço antes de trocar os binários.
$svc = Get-Service -Name $svcName -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne 'Stopped') {
    Stop-Service -Name $svcName -Force
}

# 2. Binários.
New-Item -ItemType Directory -Force -Path $dest | Out-Null
foreach ($b in 'blockd.exe', 'blockctl.exe', 'blockd-gui.exe', 'blockd-setup.exe') {
    $src = Join-Path $Source $b
    if (Test-Path $src) { Copy-Item -Force $src $dest }
}

# 3. Estado: só SYSTEM e Administradores escrevem; os usuários só leem.
New-Item -ItemType Directory -Force -Path $state | Out-Null
icacls $state /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' '*S-1-5-32-545:(OI)(CI)RX' | Out-Null
if ($LASTEXITCODE -ne 0) { throw "icacls falhou em $state" }

# 4. Grupo local com acesso ao socket (o grupo blockd do Linux).
if (-not (Get-LocalGroup -Name 'blockd' -ErrorAction SilentlyContinue)) {
    New-LocalGroup -Name 'blockd' -Description 'Pode controlar o blockd (blockctl e GUI)' | Out-Null
}
if ($User) {
    $member = Get-LocalGroupMember -Group 'blockd' -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -like "*\$User" }
    if (-not $member) { Add-LocalGroupMember -Group 'blockd' -Member $User }
}

# 5. PATH do sistema, para blockctl funcionar em qualquer terminal.
$machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
if (($machinePath -split ';') -notcontains $dest) {
    [Environment]::SetEnvironmentVariable('Path', "$machinePath;$dest", 'Machine')
}

# 6. Serviço: início automático, LocalSystem, reinicia se cair.
if (-not $svc) {
    $bin = '"' + (Join-Path $dest 'blockd.exe') + '"'
    New-Service -Name $svcName -BinaryPathName $bin -DisplayName 'blockd' `
        -Description 'Bloqueio de apps e sites (blockd)' -StartupType Automatic | Out-Null
}
sc.exe failure $svcName reset= 86400 actions= restart/5000/restart/5000/restart/30000 | Out-Null
Start-Service -Name $svcName

# 7. Atalho da GUI no menu Iniciar.
$gui = Join-Path $dest 'blockd-gui.exe'
if (Test-Path $gui) {
    $lnk = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\blockd.lnk'
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($lnk)
    $shortcut.TargetPath = $gui
    $shortcut.WorkingDirectory = $dest
    $shortcut.Save()
}

# 8. Entrada em Configurações > Aplicativos, quando veio pelo blockd-setup.exe
#    (ele mesmo faz a remoção e pede permissão de administrador).
$setup = Join-Path $dest 'blockd-setup.exe'
$uninstKey = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\blockd'
if (Test-Path $setup) {
    New-Item -Force -Path $uninstKey | Out-Null
    $values = @{
        DisplayName     = 'blockd'
        Publisher       = 'blockd'
        DisplayIcon     = Join-Path $dest 'blockd.exe'
        InstallLocation = $dest
        UninstallString = '"' + $setup + '" -uninstall'
        URLInfoAbout    = 'https://github.com/carlos-frey/blockd'
    }
    if ($Version) { $values.DisplayVersion = $Version }
    foreach ($k in $values.Keys) {
        New-ItemProperty -Force -Path $uninstKey -Name $k -Value $values[$k] -PropertyType String | Out-Null
    }
    foreach ($k in 'NoModify', 'NoRepair') {
        New-ItemProperty -Force -Path $uninstKey -Name $k -Value 1 -PropertyType DWord | Out-Null
    }
}

Write-Host ''
Write-Host 'Pronto. O serviço blockd está rodando.'
if ($User) {
    Write-Host "  - $User entrou no grupo local blockd: saia e entre de novo na sessão para valer."
}
Write-Host '  - Num terminal novo: blockctl status'
Write-Host "  - Log do serviço: $state\blockd.log"
