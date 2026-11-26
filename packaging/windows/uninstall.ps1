#Requires -RunAsAdministrator
<#
.SYNOPSIS
    Remove o blockd do Windows. Tira todos os bloqueios do sistema (filtros
    WFP e regras NRPT), mas mantém o estado salvo em ProgramData.
#>
$ErrorActionPreference = 'Stop'

$dest    = Join-Path $env:ProgramFiles 'blockd'
$state   = Join-Path $env:ProgramData 'blockd'
$svcName = 'blockd'

$svc = Get-Service -Name $svcName -ErrorAction SilentlyContinue
if ($svc -and $svc.Status -ne 'Stopped') {
    Stop-Service -Name $svcName -Force
}

# Parar o serviço não libera nada: os filtros são persistentes.
$exe = Join-Path $dest 'blockd.exe'
if (Test-Path $exe) {
    & $exe teardown
    if ($LASTEXITCODE -ne 0) { Write-Warning 'blockd teardown falhou; veja a mensagem acima' }
}

if ($svc) { sc.exe delete $svcName | Out-Null }

$lnk = Join-Path $env:ProgramData 'Microsoft\Windows\Start Menu\Programs\blockd.lnk'
Remove-Item -Force -ErrorAction SilentlyContinue $lnk
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $dest
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\blockd'

$machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
$kept = ($machinePath -split ';') | Where-Object { $_ -and $_ -ne $dest }
[Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'Machine')

Remove-LocalGroup -Name 'blockd' -ErrorAction SilentlyContinue

Write-Host "blockd removido. Estado mantido em $state (apague à mão se quiser)."
