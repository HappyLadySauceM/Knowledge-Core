param(
    [Parameter(Mandatory = $true)]
    [string]$Version,

    [Parameter(Mandatory = $true)]
    [string]$OutputDirectory
)

$ErrorActionPreference = "Stop"

$repositoryRoot = Split-Path -Parent $PSScriptRoot
$resolvedOutput = [System.IO.Path]::GetFullPath((Join-Path $repositoryRoot $OutputDirectory))
New-Item -ItemType Directory -Path $resolvedOutput -Force | Out-Null

$env:GOBIN = $resolvedOutput
& go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$Version"
if ($LASTEXITCODE -ne 0) {
    throw "golangci-lint installation failed with exit code $LASTEXITCODE"
}
