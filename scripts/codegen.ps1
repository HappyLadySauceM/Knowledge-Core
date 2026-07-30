param(
    [switch]$Check
)

$ErrorActionPreference = "Stop"
$repositoryRoot = Split-Path -Parent $PSScriptRoot
$module = "github.com/HappyLadySauce/Knowledge-Core"
$kitexVersion = "v0.16.2"
$hzVersion = "v0.9.7"
$thriftgoVersion = "0.4.5"
$ownedManifest = "scripts/generated-files.txt"

function Assert-NativeSuccess {
    param([Parameter(Mandatory = $true)][string]$Operation)

    if ($LASTEXITCODE -ne 0) {
        throw "$Operation failed with exit code $LASTEXITCODE"
    }
}

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

function Get-OwnedFiles {
    param([Parameter(Mandatory = $true)][string]$Root)

    $result = @()
    $kitexRoot = Join-Path $Root "kitex_gen"
    if (Test-Path -LiteralPath $kitexRoot) {
        $result += Get-ChildItem -LiteralPath $kitexRoot -Recurse -File
    }
    $modelRoot = Join-Path $Root "services/gateway/biz/model"
    if (Test-Path -LiteralPath $modelRoot) {
        $result += Get-ChildItem -LiteralPath $modelRoot -Recurse -File
    }
    foreach ($relative in @(".hz", "services/gateway/biz/router/gateway/gateway.go", "services/gateway/biz/router/register.go")) {
        $path = Join-Path $Root $relative
        if (Test-Path -LiteralPath $path) {
            $result += Get-Item -LiteralPath $path
        }
    }
    $prefix = $Root.TrimEnd("\", "/") + [System.IO.Path]::DirectorySeparatorChar
    return @($result | ForEach-Object { $_.FullName.Substring($prefix.Length).Replace("\", "/") } | Sort-Object -Unique)
}

function Assert-OwnedManifest {
    param([Parameter(Mandatory = $true)][string]$Root)

    $manifestPath = Join-Path $Root $ownedManifest
    $expected = @(Get-Content -LiteralPath $manifestPath | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" } | Sort-Object -Unique)
    $actual = @(Get-OwnedFiles -Root $Root)
    $difference = @(Compare-Object -ReferenceObject $expected -DifferenceObject $actual)
    if ($difference.Count -ne 0) {
        $difference | ForEach-Object { Write-Error "generated manifest mismatch: $($_.InputObject) $($_.SideIndicator)" }
        throw "generated output does not match $ownedManifest"
    }
}

function Get-GeneratedSnapshot {
    param([Parameter(Mandatory = $true)][string]$Root)

    $snapshot = @{}
    foreach ($relative in Get-OwnedFiles -Root $Root) {
        $snapshot[$relative] = Get-SHA256 -Path (Join-Path $Root $relative)
    }
    foreach ($manualRoot in @("services/gateway/biz/handler", "services/gateway/biz/router/gateway/middleware.go", "services/gateway/biz/router/router.go")) {
        $path = Join-Path $Root $manualRoot
        if (Test-Path -LiteralPath $path -PathType Leaf) {
            $snapshot[$manualRoot] = Get-SHA256 -Path $path
        } elseif (Test-Path -LiteralPath $path) {
            $prefix = $Root.TrimEnd("\", "/") + [System.IO.Path]::DirectorySeparatorChar
            Get-ChildItem -LiteralPath $path -Recurse -File | ForEach-Object {
                $relative = $_.FullName.Substring($prefix.Length).Replace("\", "/")
                $snapshot[$relative] = Get-SHA256 -Path $_.FullName
            }
        }
    }
    return $snapshot
}

function Assert-SnapshotsEqual {
    param(
        [Parameter(Mandatory = $true)][hashtable]$Expected,
        [Parameter(Mandatory = $true)][hashtable]$Actual
    )

    $changed = @(
        @($Expected.Keys) + @($Actual.Keys) |
            Sort-Object -Unique |
            Where-Object {
                -not $Expected.ContainsKey($_) -or
                -not $Actual.ContainsKey($_) -or
                $Expected[$_] -ne $Actual[$_]
            }
    )
    if ($changed.Count -ne 0) {
        $changed | ForEach-Object { Write-Error "generated file changed: $_" }
        throw "generated code is not up to date"
    }
}

function Invoke-Generation {
    param([Parameter(Mandatory = $true)][string]$Root)

    Push-Location $Root
    try {
        $actualKitexVersion = (& cmd.exe /d /c "kitex --version 2>&1" | Out-String).Trim()
        Assert-NativeSuccess -Operation "read kitex version"
        if ($actualKitexVersion -ne $kitexVersion) {
            throw "kitex version $actualKitexVersion does not match required $kitexVersion"
        }

        $actualHzVersion = (& cmd.exe /d /c "hz --version 2>&1" | Select-String -Pattern 'v[0-9]+\.[0-9]+\.[0-9]+' | ForEach-Object { $_.Matches.Value }).Trim()
        Assert-NativeSuccess -Operation "read hz version"
        if ($actualHzVersion -ne $hzVersion) {
            throw "hz version $actualHzVersion does not match required $hzVersion"
        }

        $actualThriftgoVersion = (& cmd.exe /d /c "thriftgo --version 2>&1" | Select-String -Pattern '[0-9]+\.[0-9]+\.[0-9]+' | ForEach-Object { $_.Matches.Value }).Trim()
        Assert-NativeSuccess -Operation "read thriftgo version"
        if ($actualThriftgoVersion -ne $thriftgoVersion) {
            throw "thriftgo version $actualThriftgoVersion does not match required $thriftgoVersion"
        }

        $rpcIDLs = @(& go run ./scripts/idlguard services idl/rpc/v1)
        Assert-NativeSuccess -Operation "discover RPC IDLs"
        foreach ($idl in $rpcIDLs) {
            & kitex -module $module -I idl/rpc/v1 $idl
            Assert-NativeSuccess -Operation "Kitex generation for $idl"
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
            $newHzArgs = $hzArgs + @("--router_dir", "services/gateway/biz/router", "--service", "gateway")
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
        Assert-NativeSuccess -Operation "Hertz generation"

        & gofmt -w kitex_gen services/gateway/biz
        Assert-NativeSuccess -Operation "format generated code"
    } finally {
        Pop-Location
    }
}

if (-not $Check) {
    Invoke-Generation -Root $repositoryRoot
    Assert-OwnedManifest -Root $repositoryRoot
    exit 0
}

Assert-OwnedManifest -Root $repositoryRoot
$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("knowledge-core-codegen-" + [System.Guid]::NewGuid().ToString("N"))
[System.IO.Directory]::CreateDirectory($temporaryRoot) | Out-Null
try {
    Get-ChildItem -LiteralPath $repositoryRoot -Force |
        Where-Object { $_.Name -ne ".git" } |
        Copy-Item -Destination $temporaryRoot -Recurse -Force

    foreach ($relative in @("kitex_gen", "services/gateway/biz/model")) {
        $path = Join-Path $temporaryRoot $relative
        if (Test-Path -LiteralPath $path) {
            Remove-Item -LiteralPath $path -Recurse -Force
        }
    }
    Invoke-Generation -Root $temporaryRoot
    Assert-OwnedManifest -Root $temporaryRoot
    Assert-SnapshotsEqual -Expected (Get-GeneratedSnapshot -Root $repositoryRoot) -Actual (Get-GeneratedSnapshot -Root $temporaryRoot)
} finally {
    $resolvedTemporary = [System.IO.Path]::GetFullPath($temporaryRoot)
    $resolvedSystemTemporary = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedTemporary.StartsWith($resolvedSystemTemporary) -and (Split-Path -Leaf $resolvedTemporary).StartsWith("knowledge-core-codegen-")) {
        Remove-Item -LiteralPath $resolvedTemporary -Recurse -Force -ErrorAction SilentlyContinue
    }
}
