$ErrorActionPreference = 'Stop'
# Adds fabricated API keys to the demo instance so the console screenshot shows a
# populated table. These are throwaway values generated locally; nothing here is
# valid anywhere.
$BASE = 'http://127.0.0.1:4799'
$PASSWORD = 'xh7Qm2Lp9Zr4Tn8Vb6Kd'

$s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
Invoke-WebRequest -Uri "$BASE/api/admin/login" -Method POST -WebSession $s -ContentType 'application/json' `
  -Body (@{ password = $PASSWORD } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 10 | Out-Null

foreach ($name in @('default', 'codex-client', 'claude-code-client')) {
  try {
    $r = Invoke-WebRequest -Uri "$BASE/api/admin/keys" -Method POST -WebSession $s -ContentType 'application/json' `
      -Body (@{ name = $name } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 15
    Write-Output "created $name -> HTTP $($r.StatusCode)"
  } catch {
    Write-Output "create $name failed: $($_.Exception.Message)"
  }
}

$k = Invoke-RestMethod -Uri "$BASE/api/admin/keys" -WebSession $s -TimeoutSec 15
Write-Output "keys now: $($k.keys.Count)"
foreach ($x in $k.keys) { Write-Output "  $($x.name) $($x.prefix)" }