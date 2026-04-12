param(
    [Parameter(Position = 0)]
    [string]$Directory,

    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$ForwardArgs
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = Resolve-Path (Join-Path $ScriptDir "..")

function Select-TargetDirectory {
    Add-Type -AssemblyName System.Windows.Forms | Out-Null
    $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
    $dialog.Description = "Choose an image directory"
    $dialog.UseDescriptionForTitle = $true

    if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
        return $dialog.SelectedPath
    }

    return $null
}

function Invoke-WebPGuard {
    param(
        [Parameter(Mandatory = $true)]
        [string]$TargetDir,

        [string[]]$ArgsToForward
    )

    $binaryCandidates = @(
        (Join-Path $RepoRoot "webp-guard.exe"),
        (Join-Path $RepoRoot "webp-guard")
    )

    $goCommand = Get-Command go -ErrorAction SilentlyContinue
    if ($null -ne $goCommand) {
        Push-Location $RepoRoot
        try {
            & go run . bulk --dir $TargetDir @ArgsToForward
        }
        finally {
            Pop-Location
        }
        return
    }

    foreach ($candidate in $binaryCandidates) {
        if (Test-Path $candidate) {
            & $candidate bulk --dir $TargetDir @ArgsToForward
            return
        }
    }

    $pathBinary = Get-Command webp-guard -ErrorAction SilentlyContinue
    if ($null -ne $pathBinary) {
        & $pathBinary.Source bulk --dir $TargetDir @ArgsToForward
        return
    }

    throw "webp-guard requires either Go (for go run) or a built webp-guard binary."
}

if ([string]::IsNullOrWhiteSpace($Directory)) {
    $Directory = Select-TargetDirectory
}

if ([string]::IsNullOrWhiteSpace($Directory)) {
    $Directory = Read-Host "Directory to process"
}

if ([string]::IsNullOrWhiteSpace($Directory)) {
    throw "No directory selected."
}

if (-not (Test-Path $Directory -PathType Container)) {
    throw "Directory not found: $Directory"
}

Invoke-WebPGuard -TargetDir $Directory -ArgsToForward $ForwardArgs
