# Runs a fixed battery of real, diverse tasks through every agent and reports
# how the gateway behaved on each: duration, exit code, and any anomaly the
# trace exposes (upstream retries, empty output, errors).
#
#   .\suite.ps1
#   .\suite.ps1 -Agent opencode -Task toolchain,reasoning
[CmdletBinding()]
param(
    [string[]]$Agent = @('opencode', 'codex', 'claude-code'),
    [string[]]$Task = @('toolchain', 'reasoning', 'json', 'longform', 'multistep'),
    [string]$Model = 'gpt-5.6-sol',
    [int]$TimeoutSeconds = 420,
    [string]$Gateway = 'http://127.0.0.1:4141'
)

$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
$agents = @($Agent | ForEach-Object { $_ -split ',' } | Where-Object { $_ })
$tasks = @($Task | ForEach-Object { $_ -split ',' } | Where-Object { $_ })

# Each task is deliberately different in shape so a passing run exercises more
# than one code path: tool round-trips, pure reasoning, structured output,
# long streaming output, and a multi-file build-fix loop.
$catalog = [ordered]@{
    toolchain = 'Create a file fizzbuzz.py that prints FizzBuzz for the numbers 1 through 15 (Fizz for multiples of 3, Buzz for multiples of 5, FizzBuzz for multiples of both). Run it with python and report the exact output.'
    reasoning = 'A farmer has 17 sheep and all but 9 run away. Separately, three friends split a restaurant bill of 87 dollars evenly and tip 18 percent on the total. Think through both problems step by step, then state the final number for each on its own line.'
    json      = 'Inspect the files in the current directory and output ONLY a single JSON object with exactly three keys: name (the project or directory name), files (the number of top-level files), and languages (an array of programming languages you can infer). No prose, no markdown fences.'
    longform  = 'Write a clear 400-word explanation of how a TCP three-way handshake works, aimed at a junior developer. End with a section titled "Summary" containing exactly three bullet points.'
    multistep = 'Create two files: helper.py with a function add(a, b) that returns the sum, and main.py that imports helper and prints add(21, 21). Run main.py with python, and if it fails, fix the problem and run it again until it prints the correct result. Report the final output.'
}

$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$runRoot = Join-Path $here "runs\$stamp\suite"
New-Item -ItemType Directory -Force -Path $runRoot | Out-Null

$rows = @()
foreach ($t in $tasks) {
    if (-not $catalog.Contains($t)) { throw "unknown task: $t" }
    foreach ($a in $agents) {
        Write-Host "== $a / $t =="
        $out = & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $here 'invoke.ps1') `
            -Name "$t-$a" -Agent $a -Model $Model -Prompt $catalog[$t] -TimeoutSeconds $TimeoutSeconds -Gateway $Gateway 2>&1
        $runDir = ($out | Select-String -Pattern '^run dir: ' | ForEach-Object { $_.Line.Substring(9).Trim() } | Select-Object -Last 1)
        $sessionDir = $null
        if ($runDir -and (Test-Path $runDir)) {
            $sessionDir = (Get-ChildItem $runDir -Directory | Select-Object -First 1).FullName
        }
        $exit = $null; $dur = $null; $retries = 0; $errors = 0; $textLen = 0
        if ($sessionDir) {
            $trace = Join-Path $sessionDir 'trace.jsonl'
            if (Test-Path $trace) {
                foreach ($line in [System.IO.File]::ReadLines($trace)) {
                    if ($line -match '"finished"') {
                        try { $r = $line | ConvertFrom-Json; $exit = $r.exit_code; $dur = $r.duration_ms } catch { }
                    }
                    if ($line -match 'api_retry') { $retries++ }
                    if ($line -match '"type":"error"|"is_error":true') { $errors++ }
                    if ($line -match '"type":"text"|"type":"agent_message"') { $textLen += $line.Length }
                }
            }
        }
        $rows += [pscustomobject]@{ agent = $a; task = $t; exit = $exit; ms = $dur; retries = $retries; errors = $errors; text = $textLen; dir = $sessionDir }
    }
}

$rows | Format-Table -AutoSize | Out-String | Write-Host
$rows | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $runRoot 'report.json') -Encoding UTF8
Write-Host "report: $(Join-Path $runRoot 'report.json')"

$bad = @($rows | Where-Object { $_.exit -ne 0 -or $_.retries -gt 0 -or $_.errors -gt 0 })
if ($bad.Count -gt 0) {
    Write-Host ""
    Write-Host "== anomalies =="
    $bad | ForEach-Object { Write-Host ("  {0}/{1}: exit={2} retries={3} errors={4}" -f $_.agent, $_.task, $_.exit, $_.retries, $_.errors) }
    exit 1
}
