[CmdletBinding()]
param(
    [switch]$Check,
    [switch]$IncludeHertz
)

$ErrorActionPreference = "Stop"
$repositoryRoot = [System.IO.Path]::GetFullPath((Split-Path -Parent $PSScriptRoot))
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
    if ($match.Value -ne $Expected) {
        throw "$Command version $($match.Value) does not match required $Expected"
    }
}

function Get-SHA256 {
    param([Parameter(Mandatory = $true)][string]$Path)

    return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash
}

function Get-OwnedFiles {
    param([Parameter(Mandatory = $true)][string]$Root)

    $kitexRoot = Join-Path $Root "kitex_gen"
    if (-not (Test-Path -LiteralPath $kitexRoot)) {
        return @()
    }
    $prefix = $Root.TrimEnd("\", "/") + [System.IO.Path]::DirectorySeparatorChar
    return @(Get-ChildItem -LiteralPath $kitexRoot -Recurse -File |
        ForEach-Object { $_.FullName.Substring($prefix.Length).Replace("\", "/") } |
        Sort-Object -Unique)
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
        throw "generated RPC code is not up to date"
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

if ($Check -and $IncludeHertz) {
    throw "-Check currently covers the committed Kitex contract only; Hertz ownership begins when the Gateway scaffold lands"
}

if (-not $Check) {
    if ($IncludeHertz -and -not (Test-Path -LiteralPath (Join-Path $repositoryRoot "services/gateway/biz/router/register.go"))) {
        throw "Hertz scaffold is not present. Refusing 'hz new' because it would own service source; land the Gateway transport first, then rerun with -IncludeHertz."
    }
    Invoke-RPCGeneration -Root $repositoryRoot
    Assert-OwnedManifest -Root $repositoryRoot
    if ($IncludeHertz) {
        Invoke-HertzGeneration -Root $repositoryRoot
    }
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

    Invoke-RPCGeneration -Root $temporaryRoot
    Assert-OwnedManifest -Root $temporaryRoot
    Assert-SnapshotsEqual -Expected (Get-GeneratedSnapshot -Root $repositoryRoot) -Actual (Get-GeneratedSnapshot -Root $temporaryRoot)
} finally {
    $resolvedTemporary = [System.IO.Path]::GetFullPath($temporaryRoot)
    $resolvedSystemTemporary = [System.IO.Path]::GetFullPath([System.IO.Path]::GetTempPath())
    if ($resolvedTemporary.StartsWith($resolvedSystemTemporary) -and (Split-Path -Leaf $resolvedTemporary).StartsWith("knowledge-core-codegen-")) {
        Remove-Item -LiteralPath $resolvedTemporary -Recurse -Force -ErrorAction SilentlyContinue
    }
}
