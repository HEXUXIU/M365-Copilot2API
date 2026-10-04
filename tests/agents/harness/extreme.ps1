# Extreme-scale tasks: clone a real repository (or ask for a book-length
# document) and hand the agent only a vague, novice-level goal. The prompt is
# deliberately minimal; the difficulty lives in the task, so a run forces many
# tool calls and sustained gateway traffic.
#
#   .\extreme.ps1 -Task optimize-self -Agent opencode
[CmdletBinding()]
param(
    [string[]]$Task = @('optimize-newapi', 'audit-pymc', 'optimize-self', 'harness', 'novel'),
    [string]$Agent = 'opencode',
    [string]$Model = 'gpt-5.6-sol',
    [int]$TimeoutSeconds = 3600,
    [string]$Gateway = 'http://127.0.0.1:4141',
    [string]$CacheRoot
)

$ErrorActionPreference = 'Stop'
$here = $PSScriptRoot
if ([string]::IsNullOrWhiteSpace($CacheRoot)) { $CacheRoot = Join-Path $here 'repos' }
$tasks = @($Task | ForEach-Object { $_ -split ',' } | Where-Object { $_ })

# repo = git URL to seed, or $null for a self-contained writing task.
$catalog = [ordered]@{
    'optimize-newapi' = @{ repo = 'https://github.com/lza6/new-api-Max/'; prompt = '优化这个项目' }
    'audit-pymc'      = @{ repo = 'https://github.com/ZerexaNet/PYMC'; prompt = '找出这个项目的安全漏洞并修复' }
    'optimize-self'   = @{ repo = 'https://github.com/HEXUXIU/M365-Copilot2API'; prompt = '优化这个项目' }
    'harness'         = @{ repo = 'https://github.com/deepseek-ai/deepseek-harness'; prompt = '分析这个项目并找出所有可以改进的点' }
    'novel'           = @{ repo = $null; prompt = '写一部一百万字的长篇小说，直接写正文，不要停，写到一个文件里' }
}

if (-not (Test-Path -LiteralPath $CacheRoot)) { New-Item -ItemType Directory -Force -Path $CacheRoot | Out-Null }

function Ensure-Repo($url) {
    $name = ($url -replace 'https://github.com/', '') -replace '[^a-zA-Z0-9]', '-'
    $dir = Join-Path $CacheRoot $name
    if (-not (Test-Path (Join-Path $dir '.git'))) {
        Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
        Write-Host "cloning $url ..."
        git -c core.longpaths=true clone --depth 1 $url $dir 2>$null | Out-Null
    }
    return $dir
}

foreach ($t in $tasks) {
    if (-not $catalog.Contains($t)) { throw "unknown task: $t" }
    $def = $catalog[$t]
    $seed = $null
    if ($def.repo) { $seed = Ensure-Repo $def.repo }

    Write-Host "== $Agent / $t (prompt: $($def.prompt)) =="
    $args = @(
        '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', (Join-Path $here 'invoke.ps1'),
        '-Name', "$t-$Agent", '-Agent', $Agent, '-Model', $Model,
        '-Prompt', $def.prompt, '-TimeoutSeconds', $TimeoutSeconds, '-Gateway', $Gateway
    )
    if ($seed) { $args += @('-Seed', $seed) }
    & powershell.exe @args 2>&1 | Select-Object -Last 12 | ForEach-Object { Write-Host $_ }
}
