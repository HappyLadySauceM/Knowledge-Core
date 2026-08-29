[CmdletBinding()]
param(
    [switch]$Check,
    [switch]$IncludeHertz,
    [ValidateSet("All", "Go", "Rust")]
    [string]$Scope = "All"
)

$ErrorActionPreference = "Stop"
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
$module = "github.com/HappyLadySauce/Knowledge-Core"
$kitexVersion = "v0.16.2"
$hzVersion = "v0.9.7"
$thriftgoVersion = "0.4.5"
$rustVersion = "1.97.1"
$ownedManifest = "scripts/generated-files.txt"
$generateGo = $Scope -ne "Rust"
$generateRust = $Scope -ne "Go"

function Assert-NativeSuccess {
    param([Parameter(Mandatory = $true)][string]$Operation)

    if ($LASTEXITCODE -ne 0) {
        throw "$Operation failed with exit code $LASTEXITCODE"
    }
}

function Assert-ToolVersion {
    param(
        [Parameter(Mandatory = $true)][string]$Command,
        [Parameter(Mandatory = $true)][string]$Pattern,
        [Parameter(Mandatory = $true)][string]$Expected
    )

    $previousErrorActionPreference = $ErrorActionPreference
    try {
        # Some generators report their version on stderr even when they exit
        # successfully. Windows PowerShell promotes that stderr output to an
        # error record, so do not let the script-wide Stop policy abort before
        # the native exit code can be checked.
        $ErrorActionPreference = "Continue"
        $output = (& $Command --version 2>&1 | Out-String).Trim()
        Assert-NativeSuccess -Operation "read $Command version"
    } finally {
        $ErrorActionPreference = $previousErrorActionPreference
    }
    $match = [regex]::Match($output, $Pattern)
    if (-not $match.Success) {
        throw "could not parse $Command version from: $output"
    }
    if (-not (Test-VersionAtLeast -Actual $match.Value -Minimum $Expected)) {
        throw "$Command version $($match.Value) is older than required $Expected"
    }
}

# Return true when HAVE is a semantic version greater than or equal to NEED.
# HAVE 语义版本不低于 NEED 时返回 true。
function Test-VersionAtLeast {
    param(
        [Parameter(Mandatory = $true)][string]$Actual,
        [Parameter(Mandatory = $true)][string]$Minimum
    )

    $actualVersion = [version]($Actual.TrimStart("v"))
    $minimumVersion = [version]($Minimum.TrimStart("v"))
    return $actualVersion -ge $minimumVersion
}

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    $stream = [System.IO.File]::OpenRead($Path)
    try {
        $sha256 = [System.Security.Cryptography.SHA256]::Create()
        try {
            $hash = $sha256.ComputeHash($stream)
            return ([System.BitConverter]::ToString($hash)).Replace("-", "")
        } finally {
            $sha256.Dispose()
        }
    } finally {
        $stream.Dispose()
    }
}

function Get-OwnedFiles {
    param([Parameter(Mandatory = $true)][string]$Root)

    $owned = @()
    $kitexRoot = Join-Path $Root "kitex_gen"
    if (Test-Path -LiteralPath $kitexRoot) {
        $prefix = $Root.TrimEnd("\", "/") + [System.IO.Path]::DirectorySeparatorChar
        $owned += @(Get-ChildItem -LiteralPath $kitexRoot -Recurse -File |
            ForEach-Object { $_.FullName.Substring($prefix.Length).Replace("\", "/") })
    }
    foreach ($relative in @(
        "services/gateway/biz/model/gateway/gateway.go",
        "services/gateway/biz/router/gateway/gateway.go",
        "services/gateway/biz/router/register.go",
        "services/collaboration/src/generated/mod.rs",
        "services/collaboration/src/generated/volo_gen.rs"
    )) {
        if (Test-Path -LiteralPath (Join-Path $Root $relative)) {
            $owned += $relative
        }
    }
    return @($owned | Sort-Object -Unique)
}

function Invoke-RustGeneration {
    param([Parameter(Mandatory = $true)][string]$Root)

    $previousCargoTarget = $env:CARGO_TARGET_DIR
    if ([string]::IsNullOrWhiteSpace($previousCargoTarget)) {
        $env:CARGO_TARGET_DIR = Join-Path $repositoryRoot "services/collaboration/target/codegen"
    }
    $generatorRoot = Join-Path $repositoryRoot "services/collaboration"
    Push-Location $generatorRoot
    try {
        $versionOutput = (& rustc --version 2>&1 | Out-String).Trim()
        Assert-NativeSuccess -Operation "read rustc version"
        $rustcMatch = [regex]::Match($versionOutput, "rustc ([0-9]+\.[0-9]+\.[0-9]+)")
        if (-not $rustcMatch.Success) {
            throw "could not parse rustc version from: $versionOutput"
        }
        if (-not (Test-VersionAtLeast -Actual $rustcMatch.Groups[1].Value -Minimum $rustVersion)) {
            throw "rustc version $($rustcMatch.Groups[1].Value) is older than required $rustVersion`: $versionOutput"
        }
        & cargo build --locked -p knowledge-core-rust-codegen
        Assert-NativeSuccess -Operation "Rust Thrift generation"
        $generatorBinary = Join-Path $env:CARGO_TARGET_DIR "debug/knowledge-core-rust-codegen.exe"
        if (-not (Test-Path -LiteralPath $generatorBinary)) {
            $generatorBinary = Join-Path $env:CARGO_TARGET_DIR "debug/knowledge-core-rust-codegen"
        }
        if (-not (Test-Path -LiteralPath $generatorBinary)) {
            throw "Rust codegen binary is missing: $generatorBinary"
        }
        & $generatorBinary --root $Root
        Assert-NativeSuccess -Operation "Rust Thrift generation"
        Pop-Location
        Push-Location (Join-Path $Root "services/collaboration")
        & rustfmt --edition 2024 src/generated/mod.rs src/generated/volo_gen.rs
        Assert-NativeSuccess -Operation "format Rust generated code"
    } finally {
        Pop-Location
        $env:CARGO_TARGET_DIR = $previousCargoTarget
    }
}

function Assert-OwnedManifest {
    param([Parameter(Mandatory = $true)][string]$Root)

    $manifestPath = Join-Path $Root $ownedManifest
    if (-not (Test-Path -LiteralPath $manifestPath)) {
        throw "generated manifest is missing: $ownedManifest"
    }
    $expected = @(Get-Content -LiteralPath $manifestPath |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ -ne "" -and -not $_.StartsWith("#") } |
        Sort-Object -Unique)
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

function Clear-RPCOutput {
    param([Parameter(Mandatory = $true)][string]$Root)

    $resolvedRoot = [System.IO.Path]::GetFullPath($Root).TrimEnd("\", "/")
    $target = [System.IO.Path]::GetFullPath((Join-Path $resolvedRoot "kitex_gen"))
    $expected = $resolvedRoot + [System.IO.Path]::DirectorySeparatorChar + "kitex_gen"
    if ($target -ne $expected) {
        throw "refusing to remove unexpected generated path: $target"
    }
    if (Test-Path -LiteralPath $target) {
        Remove-Item -LiteralPath $target -Recurse -Force
    }
}

function Invoke-RPCGeneration {
    param([Parameter(Mandatory = $true)][string]$Root)

    Assert-ToolVersion -Command "kitex" -Pattern 'v[0-9]+\.[0-9]+\.[0-9]+' -Expected $kitexVersion
    Assert-ToolVersion -Command "thriftgo" -Pattern '[0-9]+\.[0-9]+\.[0-9]+' -Expected $thriftgoVersion
    Clear-RPCOutput -Root $Root

    Push-Location $Root
    try {
        $rpcIDLs = @(& go run ./scripts/idlguard services idl/rpc/v1)
        Assert-NativeSuccess -Operation "discover RPC IDLs"
        if ($rpcIDLs.Count -eq 0) {
            throw "no service-bearing RPC IDLs found"
        }
        foreach ($idl in $rpcIDLs) {
            & kitex -module $module -I idl/rpc/v1 $idl
            Assert-NativeSuccess -Operation "Kitex generation for $idl"
        }
        & gofmt -w kitex_gen
        Assert-NativeSuccess -Operation "format Kitex generated code"
    } finally {
        Pop-Location
    }
}

function Invoke-HertzGeneration {
    param([Parameter(Mandatory = $true)][string]$Root)

    $register = Join-Path $Root "services/gateway/biz/router/register.go"
    if (-not (Test-Path -LiteralPath $register)) {
        throw "Hertz scaffold is not present. Refusing 'hz new' because it would own service source; land the Gateway transport first, then rerun with -IncludeHertz."
    }
    Assert-ToolVersion -Command "hz" -Pattern 'v[0-9]+\.[0-9]+\.[0-9]+' -Expected $hzVersion
    Assert-ToolVersion -Command "thriftgo" -Pattern '[0-9]+\.[0-9]+\.[0-9]+' -Expected $thriftgoVersion

    Push-Location $Root
    try {
        & hz update --module $module --idl idl/http/v1/gateway.thrift --out_dir . `
            --handler_dir services/gateway/biz/handler --model_dir services/gateway/biz/model --sort_router
        Assert-NativeSuccess -Operation "Hertz generation"
        & gofmt -w services/gateway/biz
        Assert-NativeSuccess -Operation "format Hertz generated code"
    } finally {
        Pop-Location
    }
}

if (-not $Check) {
    if ($generateGo) {
        Invoke-RPCGeneration -Root $repositoryRoot
        Invoke-HertzGeneration -Root $repositoryRoot
    }
    if ($generateRust) {
        Invoke-RustGeneration -Root $repositoryRoot
    }
    Assert-OwnedManifest -Root $repositoryRoot
    exit 0
}

Assert-OwnedManifest -Root $repositoryRoot
$temporaryRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("knowledge-core-codegen-" + [System.Guid]::NewGuid().ToString("N"))
[System.IO.Directory]::CreateDirectory($temporaryRoot) | Out-Null
try {
    foreach ($file in @("go.mod", "go.sum")) {
        $source = Join-Path $repositoryRoot $file
        if (Test-Path -LiteralPath $source) {
            Copy-Item -LiteralPath $source -Destination (Join-Path $temporaryRoot $file)
        }
    }
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "idl") -Destination (Join-Path $temporaryRoot "idl") -Recurse
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "scripts") -Destination (Join-Path $temporaryRoot "scripts") -Recurse
    if (Test-Path -LiteralPath (Join-Path $repositoryRoot "kitex_gen")) {
        Copy-Item -LiteralPath (Join-Path $repositoryRoot "kitex_gen") -Destination (Join-Path $temporaryRoot "kitex_gen") -Recurse
    }
    [System.IO.Directory]::CreateDirectory((Join-Path $temporaryRoot "services/collaboration")) | Out-Null
    foreach ($file in @("Cargo.toml", "Cargo.lock", "rust-toolchain.toml")) {
        Copy-Item -LiteralPath (Join-Path $repositoryRoot "services/collaboration/$file") -Destination (Join-Path $temporaryRoot "services/collaboration/$file")
    }
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "services/collaboration/tools") -Destination (Join-Path $temporaryRoot "services/collaboration/tools") -Recurse
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "services/collaboration/src") -Destination (Join-Path $temporaryRoot "services/collaboration/src") -Recurse
    [System.IO.Directory]::CreateDirectory((Join-Path $temporaryRoot "services/gateway")) | Out-Null
    Copy-Item -LiteralPath (Join-Path $repositoryRoot "services/gateway/biz") -Destination (Join-Path $temporaryRoot "services/gateway/biz") -Recurse
    if (Test-Path -LiteralPath (Join-Path $repositoryRoot ".hz")) {
        Copy-Item -LiteralPath (Join-Path $repositoryRoot ".hz") -Destination (Join-Path $temporaryRoot ".hz")
    }

    if ($generateGo) {
        Invoke-RPCGeneration -Root $temporaryRoot
        Invoke-HertzGeneration -Root $temporaryRoot
    }
    if ($generateRust) {
        Invoke-RustGeneration -Root $temporaryRoot
    }
    Assert-OwnedManifest -Root $temporaryRoot
    Assert-SnapshotsEqual -Expected (Get-GeneratedSnapshot -Root $repositoryRoot) -Actual (Get-GeneratedSnapshot -Root $temporaryRoot)
} finally {
    $resolvedTemporary = [System.IO.Path]::GetFullPath($temporaryRoot)
    $resolvedSystemTemporary = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedTemporary.StartsWith($resolvedSystemTemporary) -and (Split-Path -Leaf $resolvedTemporary).StartsWith("knowledge-core-codegen-")) {
        Remove-Item -LiteralPath $resolvedTemporary -Recurse -Force -ErrorAction SilentlyContinue
    }
}
