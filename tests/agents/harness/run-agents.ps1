# Drives real agents (opencode / codex) against the gateway through the godview
# logging proxy. Each session runs headless in its own working directory, so
# they can run concurrently without sharing config or state. The full external
# view of the traffic is written to <runDir>\godview\transcript-*.jsonl.
#
#   $env:M365_API_KEY = '<gateway api key>'
#   .\run-agents.ps1 -Agents opencode,codex -Concurrency 4 -Model gpt-5.6-sol `
#                    -Prompt 'Reply with exactly: OK' -Gateway http://127.0.0.1:4141
[CmdletBinding()]
param(
    [string[]]$Agents = @('opencode', 'codex'),
    [int]$Concurrency = 2,
    [string]$Model = 'gpt-5.6-sol',
    [string]$Prompt = 'Reply with exactly: OK',
    [string]$Gateway = 'http://127.0.0.1:4141',
    [int]$ProxyPort = 4142,
    [int]$TimeoutSeconds = 300,
    [string]$Key = $env:M365_API_KEY,
    [string]$RunRoot
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($RunRoot)) { $RunRoot = Join-Path $PSScriptRoot 'runs' }
# `powershell -File ... -Agents a,b` passes one string; normalize to an array.
$Agents = @($Agents | ForEach-Object { $_ -split ',' } | Where-Object { $_ -ne '' })
$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSScriptRoot))
$godview = Join-Path $PSScriptRoot 'godview.exe'
if (-not (Test-Path -LiteralPath $godview)) {
    throw "godview.exe not found at $godview; build it with: go build -o tests\agents\harness\godview.exe .\tests\agents\harness\godview"
}
if ([string]::IsNullOrWhiteSpace($Key)) {
    throw 'No API key. Set $env:M365_API_KEY or pass -Key.'
}

$ts = Get-Date -Format 'yyyyMMdd-HHmmss'
$runDir = Join-Path $RunRoot $ts
New-Item -ItemType Directory -Force -Path $runDir | Out-Null
$gvLogDir = Join-Path $runDir 'godview'
New-Item -ItemType Directory -Force -Path $gvLogDir | Out-Null
$proxyBase = "http://127.0.0.1:$ProxyPort/v1"

$gv = Start-Process -FilePath $godview -ArgumentList @(
    '-listen', "127.0.0.1:$ProxyPort", '-target', $Gateway, '-logdir', $gvLogDir, '-quiet'
) -PassThru -WindowStyle Hidden
Start-Sleep -Seconds 1

$sessions = @()
try {
    for ($i = 0; $i -lt $Concurrency; $i++) {
        $agent = $Agents[$i % $Agents.Count]
        $wd = Join-Path $runDir "session-$i-$agent"
        New-Item -ItemType Directory -Force -Path $wd | Out-Null
        $outLog = Join-Path $wd 'stdout.log'
        $errLog = Join-Path $wd 'stderr.log'

        if ($agent -eq 'opencode') {
            $cfg = @{
                '$schema' = 'https://opencode.ai/config.json'
                provider = @{
                    m365 = @{
                        npm = '@ai-sdk/openai-compatible'
                        name = 'M365 Gateway (godview)'
                        options = @{ baseURL = $proxyBase; apiKey = $Key }
                        models = @{ $Model = @{ name = $Model } }
                    }
                }
            }
            $cfg | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $wd 'opencode.json') -Encoding UTF8
            $exe = (Get-Command opencode -ErrorAction Stop).Source
            $runnerBody = @"
`$prompt = @'
$Prompt
'@
& '$exe' run --format json --model 'm365/$Model' --dir '$wd' --auto `$prompt
Set-Content -LiteralPath '$wd\exit.txt' -Value `$LASTEXITCODE
exit `$LASTEXITCODE
"@
        }
        elseif ($agent -eq 'codex') {
            $codexHome = Join-Path $wd '.codex'
            New-Item -ItemType Directory -Force -Path $codexHome | Out-Null
            $toml = @"
model = "$Model"
model_provider = "m365"
[model_providers.m365]
name = "M365 Gateway (godview)"
base_url = "$proxyBase"
env_key = "M365_API_KEY"
wire_api = "responses"
"@
            Set-Content -LiteralPath (Join-Path $codexHome 'config.toml') -Value $toml -Encoding UTF8
            $env:CODEX_HOME = $codexHome
            $env:M365_API_KEY = $Key
            $exe = (Get-Command codex -ErrorAction Stop).Source
            $runnerBody = @"
`$prompt = @'
$Prompt
'@
& '$exe' exec -m '$Model' --cd '$wd' --skip-git-repo-check `$prompt
Set-Content -LiteralPath '$wd\exit.txt' -Value `$LASTEXITCODE
exit `$LASTEXITCODE
"@
        }
        else {
            throw "unknown agent: $agent"
        }
        # The prompt goes through a runner script, not the command line, so
        # Start-Process cannot re-split it on spaces or drop quotes.
        $runner = Join-Path $wd 'runner.ps1'
        Set-Content -LiteralPath $runner -Value $runnerBody -Encoding UTF8
        $p = Start-Process -FilePath 'powershell.exe' -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $runner) `
            -WorkingDirectory $wd -NoNewWindow -PassThru -RedirectStandardOutput $outLog -RedirectStandardError $errLog
        $sessions += [pscustomobject]@{ Index = $i; Agent = $agent; Process = $p; Dir = $wd; Start = Get-Date }
    }

    foreach ($s in $sessions) {
        $exited = $s.Process.WaitForExit($TimeoutSeconds * 1000)
        if (-not $exited) {
            Stop-Process -Id $s.Process.Id -Force -ErrorAction SilentlyContinue
            $s.Process.WaitForExit()
        }
        $s.Process.Refresh()
        $exitFile = Join-Path $s.Dir 'exit.txt'
        $code = $s.Process.ExitCode
        if (Test-Path -LiteralPath $exitFile) {
            $code = [int]((Get-Content -LiteralPath $exitFile -Raw).Trim())
        }
        $s | Add-Member -NotePropertyName TimedOut -NotePropertyValue (-not $exited) -Force
        $s | Add-Member -NotePropertyName ExitCode -NotePropertyValue $code -Force
        $s | Add-Member -NotePropertyName DurationMs -NotePropertyValue ([int](New-TimeSpan -Start $s.Start -End (Get-Date)).TotalMilliseconds) -Force
    }
}
finally {
    Stop-Process -Id $gv.Id -Force -ErrorAction SilentlyContinue
}

$transcript = Get-ChildItem -LiteralPath $gvLogDir -Filter '*.jsonl' -ErrorAction SilentlyContinue | Select-Object -First 1
$summary = [pscustomobject]@{
    run_dir    = $runDir
    gateway    = $Gateway
    model      = $Model
    concurrency = $Concurrency
    transcript = $(if ($transcript) { $transcript.FullName } else { $null })
    sessions   = $sessions | ForEach-Object {
        [pscustomobject]@{ index = $_.Index; agent = $_.Agent; exit = $_.ExitCode; ms = $_.DurationMs; timed_out = [bool]$_.TimedOut; dir = $_.Dir }
    }
}
$summary | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $runDir 'summary.json') -Encoding UTF8
$summary | ConvertTo-Json -Depth 6
