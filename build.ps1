<#
.SYNOPSIS
    Builds and verifies NovaBar.

.DESCRIPTION
    The daemon's headline constraint is pure Go: no cgo, no UAC. This script asserts
    that rather than trusting it, because a stray cgo dependency introduced by a
    transitive import would silently break the promise and only show up on a user's
    machine.

.PARAMETER Task
    build   (default) build the binaries
    test    build, then run the test suite
    verify  build with CGO_ENABLED=0, vet, and test. This is what CI should run.
    clean   remove build output

.EXAMPLE
    .\build.ps1
    .\build.ps1 verify
#>
[CmdletBinding()]
param(
    [ValidateSet('build', 'test', 'verify', 'clean')]
    [string]$Task = 'build'
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$root = $PSScriptRoot
$bin  = Join-Path $root 'bin'

function Write-Step($msg) {
    Write-Host "==> $msg" -ForegroundColor Cyan
}

function Invoke-Go {
    param([string[]]$GoArgs, [hashtable]$Env = @{})
    foreach ($key in $Env.Keys) {
        Set-Item -Path "env:$key" -Value $Env[$key]
    }
    try {
        & go @GoArgs
        if ($LASTEXITCODE -ne 0) {
            throw "go $($GoArgs -join ' ') failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        # Restore whatever was there before so we never leak CGO_ENABLED into the
        # caller's shell.
        foreach ($key in $Env.Keys) {
            Remove-Item -Path "env:$key" -ErrorAction SilentlyContinue
        }
    }
}

function Test-NoCgo {
    Write-Step 'Verifying the tree builds with CGO_ENABLED=0'
    Invoke-Go -GoArgs @('build', '-o', 'bin/novabar.exe', '.') -Env @{ CGO_ENABLED = '0' }
    Invoke-Go -GoArgs @('build', '-o', 'bin/shellctl.exe', './cmd/shellctl') -Env @{ CGO_ENABLED = '0' }
    Write-Host '    OK: both binaries built with cgo disabled' -ForegroundColor Green
}

function Invoke-Build {
    Write-Step 'Building NovaBar'
    if (-not (Test-Path $bin)) {
        New-Item -ItemType Directory -Path $bin | Out-Null
    }
    Invoke-Go -GoArgs @('build', '-trimpath', '-ldflags', '-s -w', '-o', 'bin/novabar.exe', '.')
    Invoke-Go -GoArgs @('build', '-trimpath', '-ldflags', '-s -w', '-o', 'bin/shellctl.exe', './cmd/shellctl')
    Write-Host "    built bin/novabar.exe and bin/shellctl.exe" -ForegroundColor Green
}

function Invoke-Test {
    Write-Step 'Running tests'
    Invoke-Go -GoArgs @('test', './...', '-count=1')
}

function Invoke-Clean {
    Write-Step 'Cleaning build output'
    foreach ($path in @('bin', 'novabar.exe')) {
        $full = Join-Path $root $path
        if (Test-Path $full) {
            Remove-Item -Recurse -Force $full
            Write-Host "    removed $path"
        }
    }
}

Push-Location $root
try {
    switch ($Task) {
        'build'  { Invoke-Build }
        'test'   { Invoke-Build; Invoke-Test }
        'verify' { Test-NoCgo; Invoke-Test; & go vet ./...; if ($LASTEXITCODE -ne 0) { throw 'go vet failed' } }
        'clean'  { Invoke-Clean }
    }
    Write-Step "$Task complete"
}
finally {
    Pop-Location
}