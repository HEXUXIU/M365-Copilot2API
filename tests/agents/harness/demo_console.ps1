$ErrorActionPreference = 'Stop'
# Starts a throwaway console instance, populated entirely with fake data, and
# leaves it running on its own port. It never reads the production config
# directory, so no real account, key, proxy or conversation can appear in a
# screenshot.
$root = 'D:\m365-demo'
if (Test-Path $root) { Remove-Item -Recurse -Force $root }
New-Item -ItemType Directory -Force -Path "$root\data" | Out-Null
New-Item -ItemType Directory -Force -Path "$root\bin" | Out-Null

# Write without a BOM: the Go binary's JSON reader rejects a leading BOM.
# The shape must match auth.Cache / auth.AccountToken exactly.
$utf8 = [System.Text.UTF8Encoding]::new($false)

function New-DemoAccount($id, $email, $oid, $tid) {
  $lines = @(
    '    {',
    ('      "id": "' + $id + '",'),
    ('      "email": "' + $email + '",'),
    ('      "displayName": "' + $email + '",'),
    '      "status": "online",',
    '      "accessToken": "demo-access-token",',
    '      "refreshToken": "demo-refresh-token",',
    '      "expiresAt": "2099-01-01T00:00:00Z",',
    '      "updatedAt": "2099-01-01T00:00:00Z",',
    ('      "oid": "' + $oid + '",'),
    ('      "tid": "' + $tid + '"'),
    '    }'
  )
  return ($lines -join "`n")
}

$cache = @"
{
  "accounts": [
$(New-DemoAccount 'demo-1' 'demo.user@example.com'  '00000000-demo-oid-0000-000000000001' '00000000-demo-tid-0000-000000000001'),
$(New-DemoAccount 'demo-2' 'second.demo@example.com' '00000000-demo-oid-0000-000000000002' '00000000-demo-tid-0000-000000000002'),
$(New-DemoAccount 'demo-3' 'third.demo@example.com'  '00000000-demo-oid-0000-000000000003' '00000000-demo-tid-0000-000000000003')
  ]
}
"@
[System.IO.File]::WriteAllText("$root\data\accounts.json", $cache, $utf8)
[System.IO.File]::WriteAllText("$root\data\sessions.json", '[]', $utf8)
[System.IO.File]::WriteAllText("$root\data\conversations.json", '[]', $utf8)

# Copy the released binary so the demo matches the shipped build.
$exe = 'D:\M365-Copilot2API\m365-server.exe'
Copy-Item $exe "$root\bin\m365-server.exe" -Force

$env:M365_LISTEN = '127.0.0.1:4799'
$env:M365_DATA_DIR = "$root\data"
# Every store falls back to ~/.config/m365-copilot2api when its variable is
# unset, which would pull production keys and stats into a screenshot. Point
# each one at the demo directory explicitly.
$env:M365_TOKEN_CACHE = "$root\data\accounts.json"
$env:M365_SETTINGS_FILE = "$root\data\settings.json"
$env:M365_API_KEYS = "$root\data\api-keys.json"
$env:M365_SESSION_CACHE = "$root\data\sessions.json"
$env:M365_USER_SESSION_CACHE = "$root\data\user-sessions.json"
$env:M365_CONVERSATION_CACHE = "$root\data\conversations.json"
$env:M365_USAGE_LOG = "$root\data\usage.jsonl"
$env:M365_STATS_FILE = "$root\data\stats.json"

$p = Start-Process -FilePath "$root\bin\m365-server.exe" -WorkingDirectory "$root\bin" `
  -PassThru -WindowStyle Hidden `
  -RedirectStandardOutput "$root\out.log" -RedirectStandardError "$root\err.log"

Start-Sleep -Seconds 6
Write-Output "demo pid=$($p.Id)"
try {
  $r = Invoke-WebRequest -Uri 'http://127.0.0.1:4799/login' -UseBasicParsing -TimeoutSec 10
  Write-Output "login page HTTP $($r.StatusCode)"
} catch {
  Write-Output "ERR $($_.Exception.Message)"
  Get-Content "$root\err.log" -Tail 10
}