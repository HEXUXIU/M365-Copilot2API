$ErrorActionPreference = 'Stop'
# One-shot screenshot pipeline. Everything runs in a single process lifetime so
# the demo server cannot be reaped between steps.
#
# It renders the REAL built console (not a mock) against a disposable instance
# that holds fabricated data only: three example.com accounts and locally
# generated keys, in a separate directory on its own port. Nothing here reads
# the production config, so no real account, key, proxy or conversation can
# reach a published image.

$root = 'D:\m365-demo'
$port = 4799
$BASE = "http://127.0.0.1:$port"
$PASSWORD = 'xh7Qm2Lp9Zr4Tn8Vb6Kd'
$utf8 = [System.Text.UTF8Encoding]::new($false)

if (Test-Path $root) { Remove-Item -Recurse -Force $root }
New-Item -ItemType Directory -Force -Path "$root\data", "$root\bin" | Out-Null

function New-DemoAccount($id, $email) {
  @(
    '    {'
    ('      "id": "' + $id + '",')
    ('      "email": "' + $email + '",')
    ('      "displayName": "' + $email + '",')
    '      "status": "online",'
    '      "accessToken": "demo-access-token",'
    '      "refreshToken": "demo-refresh-token",'
    '      "expiresAt": "2099-01-01T00:00:00Z",'
    '      "updatedAt": "2099-01-01T00:00:00Z",'
    ('      "oid": "00000000-demo-oid-' + $id.PadLeft(12, '0') + '",')
    ('      "tid": "00000000-demo-tid-' + $id.PadLeft(12, '0') + '"')
    '    }'
  ) -join "`n"
}

$cache = '{"accounts": [' + "`n" +
  (New-DemoAccount '1' 'demo.user@example.com') + ',' + "`n" +
  (New-DemoAccount '2' 'second.demo@example.com') + ',' + "`n" +
  (New-DemoAccount '3' 'third.demo@example.com') + "`n" + ']}'
[System.IO.File]::WriteAllText("$root\data\accounts.json", $cache, $utf8)
[System.IO.File]::WriteAllText("$root\data\sessions.json", '{}', $utf8)
[System.IO.File]::WriteAllText("$root\data\conversations.json", '[]', $utf8)

Copy-Item 'D:\M365-Copilot2API\m365-server.exe' "$root\bin\m365-server.exe" -Force

# Every store falls back to ~/.config/m365-copilot2api when its variable is
# unset, which would pull production keys and stats into a screenshot.
$env:M365_LISTEN = "127.0.0.1:$port"
$env:M365_DATA_DIR = "$root\data"
$env:M365_TOKEN_CACHE = "$root\data\accounts.json"
$env:M365_SETTINGS_FILE = "$root\data\settings.json"
$env:M365_API_KEYS = "$root\data\api-keys.json"
$env:M365_SESSION_CACHE = "$root\data\sessions.json"
$env:M365_USER_SESSION_CACHE = "$root\data\user-sessions.json"
$env:M365_CONVERSATION_CACHE = "$root\data\conversations.json"
$env:M365_USAGE_LOG = "$root\data\usage.jsonl"

$proc = Start-Process -FilePath "$root\bin\m365-server.exe" -WorkingDirectory "$root\bin" `
  -PassThru -WindowStyle Hidden -RedirectStandardOutput "$root\out.log" -RedirectStandardError "$root\err.log"
Write-Output "demo pid=$($proc.Id)"
Start-Sleep -Seconds 5

try {
  # Log in with the initial password, then set a known one.
  $s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  Invoke-WebRequest -Uri "$BASE/api/admin/login" -Method POST -WebSession $s -ContentType 'application/json' `
    -Body (@{ password = 'admin123' } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 15 | Out-Null
  Invoke-WebRequest -Uri "$BASE/api/admin/change-password" -Method POST -WebSession $s -ContentType 'application/json' `
    -Body (@{ current_password = 'admin123'; new_password = $PASSWORD } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 15 | Out-Null

  $s2 = New-Object Microsoft.PowerShell.Commands.WebRequestSession
  Invoke-WebRequest -Uri "$BASE/api/admin/login" -Method POST -WebSession $s2 -ContentType 'application/json' `
    -Body (@{ password = $PASSWORD } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 15 | Out-Null

  foreach ($name in @('default', 'codex-client', 'claude-code-client')) {
    Invoke-WebRequest -Uri "$BASE/api/admin/keys" -Method POST -WebSession $s2 -ContentType 'application/json' `
      -Body (@{ name = $name } | ConvertTo-Json -Compress) -UseBasicParsing -TimeoutSec 15 | Out-Null
  }
  Write-Output 'demo seeded'

  $env:PATH = 'D:\nodejs;' + $env:PATH
  & node "$PSScriptRoot\verify_demo.js" $PASSWORD
  if ($LASTEXITCODE -ne 0) { throw 'verification found production data in a view' }
  & node "$PSScriptRoot\capture_screenshots.js" $PASSWORD
  Write-Output "capture exit=$LASTEXITCODE"
}
finally {
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  Write-Output 'demo stopped'
}