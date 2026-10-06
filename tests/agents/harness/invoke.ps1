# Runs real agents against the gateway and prints every LLM exchange.
#
# This script only orchestrates. Each agent already has a scriptable headless
# mode that emits structured JSON, so the workflow stays close to how a CI job
# would drive them and adds three things the agents do not do on their own:
#
#   1. Provider setup, so the agents talk to the gateway instead of a vendor.
#   2. Timing, so a slow gateway shows up as a number instead of a stopwatch.
#   3. One place where every exchange is collected for triage.
#
# Nothing here simulates or stubs an agent: opencode, codex and claude-code each
# run as their own process and speak their own protocol to the gateway.
[CmdletBinding()]
param(
    # Free-form scenario name, used for the run directory.
    [string]$Name = 'run',
    # opencode | codex | claude-code
    [string[]]$Agent = @('opencode', 'codex', 'claude-code'),
    # How many copies of each agent to start.
    [int]$Concurrency = 1,
    [string]$Gateway = 'http://127.0.0.1:4141',
[string]$Model = 'gpt-5.6-sol',
    # Path to a file holding the prompt. The prompt must be passed this way and
    # never as a -Prompt argument: powershell.exe -File splits a multi-line
    # argument into separate tokens, which corrupts every parameter after it and
    # ends up written into the agent's generated config.
    [string]$PromptFile,
    [int]$TimeoutSeconds = 300,
    [string]$ApiKey = $env:M365_API_KEY,
    # Optional directory copied into each session's working directory before the
    # agent starts, so a task can point at a real repository.
    [string]$Seed,
    [string]$RunRoot
)

$ErrorActionPreference = 'Stop'
if ([string]::IsNullOrWhiteSpace($RunRoot)) { $RunRoot = Join-Path $PSScriptRoot 'runs' }
if ([string]::IsNullOrWhiteSpace($PromptFile) -or -not (Test-Path -LiteralPath $PromptFile)) {
    throw 'Pass -PromptFile pointing at a file that contains the prompt text.'
}
$Prompt = [System.IO.File]::ReadAllText($PromptFile)
# `powershell -File ... -Agent a,b` passes one string; normalize to an array.
$agents = @($Agent | ForEach-Object { $_ -split ',' } | Where-Object { $_ })
if ($Concurrency -gt 1 -and $agents.Count -gt 1) {
    throw 'Use either one agent with -Concurrency, or several agents with -Concurrency 1.'
}

# Each agent gets its own directory so config, session state and logs never mix.
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$runDir = Join-Path (Join-Path $RunRoot $stamp) $Name
New-Item -ItemType Directory -Force -Path $runDir | Out-Null

if ([string]::IsNullOrWhiteSpace($ApiKey)) {
    $candidate = Join-Path $PSScriptRoot 'api-key.txt'
    if (Test-Path -LiteralPath $candidate) { $ApiKey = (Get-Content -LiteralPath $candidate -Raw).Trim() }
}
if ([string]::IsNullOrWhiteSpace($ApiKey)) {
    throw 'No API key. Set $env:M365_API_KEY, pass -ApiKey, or create tests\agents\harness\api-key.txt.'
}

$base = $Gateway.TrimEnd('/')
$nodes = @($agents)
foreach ($n in $agents) {
    for ($i = 1; $i -lt $Concurrency; $i++) { $nodes += $n }
}

$opencodeRoot = Join-Path $PSScriptRoot '..\opencode'
$codexPkg = Join-Path $PSScriptRoot '..\codex'
$claudePkg = Join-Path $PSScriptRoot '..\claude-code-restored\raw\package\cli.js'

# bun runs opencode straight from the patched source tree; there is no build step
# and no TUI, so a run is fully described by this script plus the config it writes.
$bun = (Get-Command bun -ErrorAction SilentlyContinue).Source
if (-not $bun) { $bun = 'bun' }
$node = (Get-Command node -ErrorAction SilentlyContinue).Source
if (-not $node) { $node = 'node' }

$script = @'

$ErrorActionPreference = 'Stop'
$harness = $env:OC_HARNESS
$agent = $env:OC_AGENT
$work = $env:OC_WORKDIR
$project = $env:OC_PROJECT
$model = $env:OC_MODEL
$key = $env:OC_API_KEY
$base = $env:OC_GATEWAY
$trace = Join-Path $work 'trace.jsonl'
# The prompt is read from a file, not an env var: a multi-line prompt carried in
# an environment variable is not reliably preserved across Start-Process and it
# corrupts any generated config that interpolates it.
$prompt = [System.IO.File]::ReadAllText((Join-Path $work 'prompt.txt'))

function Write-Trace($data) {
    # ConvertTo-Json rather than System.Text.Json: Windows PowerShell 5.1 has
    # no System.Text.Json assembly.
    $line = $data | ConvertTo-Json -Depth 12 -Compress
    [System.IO.File]::AppendAllText($trace, $line + [Environment]::NewLine)
}

$started = Get-Date
$exit = 0
$runError = $null
$text = ''

try {
    if ($agent -eq 'opencode') {
        $cfg = @{
            '$schema' = 'https://opencode.ai/config.json'
            provider = @{
                m365 = @{
                    npm    = '@ai-sdk/openai-compatible'
                    name   = 'M365 Gateway'
                    options = @{ baseURL = "$base/v1"; apiKey = $key }
                    models = @{ $model = @{ name = $model } }
                }
            }
        }
        [System.IO.File]::WriteAllText(
            (Join-Path $work 'opencode.json'),
            ($cfg | ConvertTo-Json -Depth 8))

# --log is our patch: a full JSONL trace of every event with timings,
        # which is what makes a failing run diagnosable after the fact.
        # Run from the repo root and let opencode chdir to the work dir.
        # No 2>&1: under $ErrorActionPreference='Stop' a merged stderr line
        # aborts the pipeline instead of being recorded.
        Push-Location $env:OC_OPENCODE_ROOT
        try {
            & bun run packages/opencode/src/index.ts `
                run --model "m365/$model" --dir $project --auto --log $trace $prompt |
                ForEach-Object { Write-Output $_ }
            $exit = $LASTEXITCODE
        } finally { Pop-Location }
    }
    elseif ($agent -eq 'codex') {
        $codexHome = Join-Path $work 'codex-home'
        New-Item -ItemType Directory -Force -Path $codexHome | Out-Null
$toml = @"
model = "$model"
model_provider = "m365"
approval_policy = "never"
sandbox_mode = "danger-full-access"

[model_providers.m365]
name = "$model via M365 Gateway"
base_url = "$base/v1"
env_key = "M365_API_KEY"
wire_api = "responses"
"@
[System.IO.File]::WriteAllText((Join-Path $codexHome 'config.toml'), $toml)
$env:CODEX_HOME = $codexHome
        $env:M365_API_KEY = $key
        # codex reads $env:HOME in places and fails hard if it is unset.
        if (-not $env:HOME) { $env:HOME = $codexHome }

# codex exec --json is already machine-readable, so it is captured as-is.
        # No 2>&1: under $ErrorActionPreference='Stop' a merged stderr line
        # would abort the pipeline instead of being recorded.
        # codex also appends anything already on stdin to the prompt, so stdin must
        # be empty: Start-Process gives it NUL, and codex reads it to EOF.
        # codex appends anything already on stdin to the prompt. The runner process is
        # started with stdin redirected to an empty file so it sees EOF at once.
        & codex exec --json --skip-git-repo-check --cd $project -m $model $prompt |
            ForEach-Object {
                Write-Output $_
                try {
                    $evt = $_ | ConvertFrom-Json
                    Write-Trace @{ agent = 'codex'; t = [int]((Get-Date) - $started).TotalMilliseconds; event = $evt }
                } catch { }
            }
        $exit = $LASTEXITCODE
    }
    elseif ($agent -eq 'claude-code') {
        # The restored 2.1.88 CLI honours the standard Anthropic env vars, so the
        # gateway's /v1/messages endpoint is reachable without patching.
        $env:ANTHROPIC_BASE_URL = $base
        $env:ANTHROPIC_API_KEY = $key
        $env:ANTHROPIC_MODEL = $model
        $env:CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC = '1'

        & node $env:OC_CLAUDE_CLI -p $prompt --output-format stream-json --verbose --add-dir $project |
            ForEach-Object {
                Write-Output $_
                try {
                    $evt = $_ | ConvertFrom-Json
                    Write-Trace @{ agent = 'claude-code'; t = [int]((Get-Date) - $started).TotalMilliseconds; event = $evt }
                    if ($evt.type -eq 'result' -and $evt.is_error) { $runError = $evt.result }
                } catch { }
            }
        $exit = $LASTEXITCODE
    }
    else {
        throw "unknown agent: $agent"
    }
}
catch {
    $runError = $_.Exception.Message
    $exit = 1
}
finally {
    Write-Trace @{
        agent        = $agent
        type         = 'finished'
        duration_ms  = [int]((Get-Date) - $started).TotalMilliseconds
        exit_code    = $exit
        error        = $runError
    }
}

if ($exit -ne 0) { exit $exit }
'@

$runner = Join-Path $runDir 'run-one.ps1'
[System.IO.File]::WriteAllText($runner, $script)

$env:OC_HARNESS = $PSScriptRoot
$env:OC_OPENCODE_ROOT = (Resolve-Path -LiteralPath $opencodeRoot).Path
$env:OC_CLAUDE_CLI = $claudePkg
$env:OC_GATEWAY = $base
$env:OC_MODEL = $Model
$env:OC_API_KEY = $ApiKey

$procs = @()
foreach ($n in $nodes) {
    $n = $n.Trim()
    $slug = ($n -replace '[^a-zA-Z0-9]', '-')
    $i = 0
    while (Test-Path (Join-Path $runDir "$slug-$i")) { $i++ }
$work = Join-Path $runDir "$slug-$i"
    New-Item -ItemType Directory -Force -Path $work | Out-Null

    # The audited project lives in its own subdirectory so an agent that
    # wanders upward still cannot touch the real repository: everything above
    # $work is harness output. Each session gets its own copy, so concurrent
    # sessions never share files.
    $project = Join-Path $work 'project'
    New-Item -ItemType Directory -Force -Path $project | Out-Null
    if ($Seed -and (Test-Path -LiteralPath $Seed)) {
        robocopy $Seed $project /E /NFL /NDL /NJH /NJS /R:1 /W:1 | Out-Null
    }

    $stdout = Join-Path $work 'stdout.log'
    $stderr = Join-Path $work 'stderr.log'
    # An empty stdin file, because codex reads stdin and appends it to the prompt.
    $stdin = Join-Path $work 'stdin.txt'
    [System.IO.File]::WriteAllText($stdin, '')
    # The prompt travels as a file so multi-line prompts survive intact.
    [System.IO.File]::WriteAllText((Join-Path $work 'prompt.txt'), $Prompt)

$env:OC_AGENT = $n
    $env:OC_WORKDIR = $work
    $env:OC_PROJECT = $project

    $procs += [pscustomobject]@{
        Agent = $n
        Dir = $work
        Stdout = $stdout
        Stderr = $stderr
        Start = Get-Date
        Process = Start-Process -FilePath 'powershell.exe' `
            -ArgumentList @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $runner) `
            -WorkingDirectory $work -PassThru -NoNewWindow `
            -RedirectStandardInput $stdin `
            -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    }
}

$timedOut = @()
foreach ($p in $procs) {
    if (-not $p.Process.WaitForExit($TimeoutSeconds * 1000)) {
        Stop-Process -Id $p.Process.Id -Force -ErrorAction SilentlyContinue
        $timedOut += $p.Agent
    }
}

$results = foreach ($p in $procs) {
    $p.Process.Refresh()
    # Start-Process -PassThru does not reliably populate ExitCode when output is
    # redirected, so the runner's own record is the source of truth.
    $exit = $null
    $runError = $null
    $traceFile = Join-Path $p.Dir 'trace.jsonl'
    if (Test-Path -LiteralPath $traceFile) {
        foreach ($line in [System.IO.File]::ReadLines($traceFile)) {
            if ($line -notmatch '"finished"') { continue }
            try {
                $rec = $line | ConvertFrom-Json
                if ($rec.exit_code -ne $null) { $exit = [int]$rec.exit_code }
                if ($rec.error) { $runError = [string]$rec.error }
            } catch { }
        }
    }
    [pscustomobject]@{
        agent = $p.Agent
        exit_code = $exit
        error = $runError
        duration_ms = [int]((Get-Date) - $p.Start).TotalMilliseconds
        timed_out = ($p.Agent -in $timedOut)
        dir = $p.Dir
    }
}

$results | Sort-Object agent | Format-Table -AutoSize | Out-String | Write-Output
Write-Output "run dir: $runDir"

$results | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $runDir 'summary.json') -Encoding UTF8

if (($results | Where-Object { $_.exit_code -ne 0 }).Count -gt 0) { exit 1 }