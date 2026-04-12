param(
    [Parameter(Position = 0)]
    [string]$Directory,

    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$ForwardArgs
)

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$RepoRoot = (Resolve-Path (Join-Path $ScriptDir "..")).Path

function Select-TargetDirectory {
    $dialog = $null
    try {
        Add-Type -AssemblyName System.Windows.Forms | Out-Null
        $dialog = New-Object System.Windows.Forms.FolderBrowserDialog
        $dialog.Description = "Choose an image directory"
        $dialog.UseDescriptionForTitle = $true

        if ($dialog.ShowDialog() -eq [System.Windows.Forms.DialogResult]::OK) {
            return $dialog.SelectedPath
        }
    }
    catch {
        return $null
    }
    finally {
        if ($null -ne $dialog) {
            $dialog.Dispose()
        }
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

    Push-Location $RepoRoot
    try {
        $goCommand = Get-Command go -ErrorAction SilentlyContinue
        if ($null -ne $goCommand) {
            & go run . bulk --dir $TargetDir @ArgsToForward
            return $LASTEXITCODE
        }

        foreach ($candidate in $binaryCandidates) {
            if (Test-Path $candidate) {
                & $candidate bulk --dir $TargetDir @ArgsToForward
                return $LASTEXITCODE
            }
        }

        $pathBinary = Get-Command webp-guard -ErrorAction SilentlyContinue
        if ($null -ne $pathBinary) {
            & $pathBinary.Source bulk --dir $TargetDir @ArgsToForward
            return $LASTEXITCODE
        }
    }
    finally {
        Pop-Location
    }

    throw "webp-guard requires either Go (for go run) or a built webp-guard binary."
}

function Resolve-TargetDirectory {
    param(
        [Parameter(Mandatory = $true)]
        [string]$Path
    )

    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $null
    }

    $candidate = $Path
    if ($candidate -eq "~") {
        $candidate = $HOME
    }
    elseif ($candidate -match '^~[\\/]') {
        $candidate = Join-Path $HOME $candidate.Substring(2)
    }

    if (-not [System.IO.Path]::IsPathRooted($candidate)) {
        $candidate = Join-Path $RepoRoot $candidate
    }

    try {
        return (Resolve-Path -LiteralPath $candidate -ErrorAction Stop).Path
    }
    catch {
        return $null
    }
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

$ResolvedDirectory = Resolve-TargetDirectory -Path $Directory
if ([string]::IsNullOrWhiteSpace($ResolvedDirectory) -or -not (Test-Path -LiteralPath $ResolvedDirectory -PathType Container)) {
    throw "Directory not found: $Directory"
}

$exitCode = Invoke-WebPGuard -TargetDir $ResolvedDirectory -ArgsToForward $ForwardArgs
exit $exitCode
