param(
    [switch]$Check
)

$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$generatedRoots = @("kitex_gen", "services/gateway/biz")
$repositoryPrefix = $repositoryRoot.TrimEnd("\", "/") + [System.IO.Path]::DirectorySeparatorChar

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    $algorithm = [System.Security.Cryptography.SHA256]::Create()
    $stream = [System.IO.File]::OpenRead($Path)
    try {
        return ([System.BitConverter]::ToString($algorithm.ComputeHash($stream))).Replace("-", "")
    } finally {
        $stream.Dispose()
        $algorithm.Dispose()
    }
}

function Get-GeneratedSnapshot {
    $snapshot = @{}
    foreach ($root in $generatedRoots) {
        $absoluteRoot = Join-Path $repositoryRoot $root
        Get-ChildItem -LiteralPath $absoluteRoot -Recurse -File | ForEach-Object {
            $relativePath = $_.FullName.Substring($repositoryPrefix.Length).Replace("\", "/")
            $snapshot[$relativePath] = Get-SHA256 -Path $_.FullName
        }
    }
    return $snapshot
}

$before = if ($Check) { Get-GeneratedSnapshot } else { $null }

Push-Location $repositoryRoot
try {
    $module = "github.com/HappyLadySauce/Knowledge-Core"
    $kitexVersion = "v0.16.2"
    $hzVersion = "v0.9.7"
    $thriftgoVersion = "0.4.5"

$actualKitexVersion = (& cmd.exe /d /c "kitex --version 2>&1" | Out-String).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "failed to read kitex version"
}
if ($actualKitexVersion -ne $kitexVersion) {
    throw "kitex version $actualKitexVersion does not match required $kitexVersion"
}

$actualHzVersion = (& cmd.exe /d /c "hz --version 2>&1" | Select-String -Pattern 'v[0-9]+\.[0-9]+\.[0-9]+' | ForEach-Object { $_.Matches.Value }).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "failed to read hz version"
}
if ($actualHzVersion -ne $hzVersion) {
    throw "hz version $actualHzVersion does not match required $hzVersion"
}

$actualThriftgoVersion = (& cmd.exe /d /c "thriftgo --version 2>&1" | Select-String -Pattern '[0-9]+\.[0-9]+\.[0-9]+' | ForEach-Object { $_.Matches.Value }).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "failed to read thriftgo version"
}
if ($actualThriftgoVersion -ne $thriftgoVersion) {
    throw "thriftgo version $actualThriftgoVersion does not match required $thriftgoVersion"
}

$rpcIDLs = @("identity", "knowledge", "platform")
foreach ($idl in $rpcIDLs) {
    & kitex -module $module -I idl/rpc "idl/rpc/$idl.thrift"
    if ($LASTEXITCODE -ne 0) {
        throw "kitex generation failed for $idl"
    }
}

$hzArgs = @(
    "--module", $module,
    "--idl", "idl/http/v1/gateway.thrift",
    "--out_dir", ".",
    "--handler_dir", "services/gateway/biz/handler",
    "--model_dir", "services/gateway/biz/model",
    "--sort_router"
)

if (Test-Path "services/gateway/biz/router/register.go") {
    & hz update @hzArgs
} else {
    $newHzArgs = $hzArgs + @(
        "--router_dir", "services/gateway/biz/router",
        "--service", "gateway"
    )
    $excluded = @(
        "--exclude_file", "main.go",
        "--exclude_file", "go.mod",
        "--exclude_file", ".gitignore",
        "--exclude_file", "router_gen.go",
        "--exclude_file", "router.go",
        "--exclude_file", "build.sh",
        "--exclude_file", "script/bootstrap.sh",
        "--exclude_file", "services\gateway\biz\handler\ping.go"
    )
    & hz new @newHzArgs @excluded
}
if ($LASTEXITCODE -ne 0) {
    throw "Hertz generation failed"
}

    gofmt -w kitex_gen services/gateway/biz
} finally {
    Pop-Location
}

if ($Check) {
    $after = Get-GeneratedSnapshot
    $changed = @(
        @($before.Keys) + @($after.Keys) |
            Sort-Object -Unique |
            Where-Object {
                -not $before.ContainsKey($_) -or
                -not $after.ContainsKey($_) -or
                $before[$_] -ne $after[$_]
            }
    )
    if ($changed.Count -ne 0) {
        $changed | ForEach-Object { Write-Error "generated file changed: $_" }
        throw "generated code is not up to date"
    }
}
