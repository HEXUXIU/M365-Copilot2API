$ErrorActionPreference = 'Stop'
# Finishes the throwaway console: changes the demo admin password, then
# verifies the instance only knows the fake accounts. It runs entirely against
# the demo port and never touches the production config.
$BASE = 'http://127.0.0.1:4799'
# The server rejects common words and anything containing the project name, so
# this is deliberately random-looking.
$NEW = 'xh7Qm2Lp9Zr4Tn8Vb6Kd'

$s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
Invoke-WebRequest -Uri "$BASE/api/admin/login" -Method POST -WebSession $s -ContentType 'application/json' `
  -Body (@{ password = 'admin123' } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 10 | Out-Null
Write-Output 'logged in with initial password'

$r = Invoke-WebRequest -Uri "$BASE/api/admin/change-password" -Method POST -WebSession $s -ContentType 'application/json' `
  -Body (@{ current_password = 'admin123'; new_password = $NEW } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 15
Write-Output "change-password HTTP $($r.StatusCode)"

$s2 = New-Object Microsoft.PowerShell.Commands.WebRequestSession
Invoke-WebRequest -Uri "$BASE/api/admin/login" -Method POST -WebSession $s2 -ContentType 'application/json' `
  -Body (@{ password = $NEW } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 10 | Out-Null

$acc = Invoke-RestMethod -Uri "$BASE/api/accounts" -WebSession $s2 -TimeoutSec 15
Write-Output "accounts: $($acc.accounts.Count)"
foreach ($a in $acc.accounts) { Write-Output "  $($a.email)  $($a.status)" }

Write-Output "PASSWORD=$NEW"