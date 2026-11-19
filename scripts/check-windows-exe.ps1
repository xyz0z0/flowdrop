# check-windows-exe.ps1 — verify the FlowDrop Windows build's stamped resources.
#
# Run on Windows 11 in the folder where flowdrop-desktop.exe was unzipped:
#
#   powershell -ExecutionPolicy Bypass -File .\check-windows-exe.ps1 .\flowdrop-desktop.exe
#
# It prints the version resource Windows will show in the file's Properties
# dialog, the SHA-256 of the binary, and extracts the embedded icon to
# exe-icon.png so it can be compared with FlowDrop's blue rounded icon.
# It only reads the file; it starts nothing and changes no settings.
param(
    [Parameter(Position = 0)]
    [string]$Exe = ".\flowdrop-desktop.exe"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $Exe)) {
    Write-Error "not found: $Exe (pass the path to flowdrop-desktop.exe)"
    exit 1
}

$file = Get-Item -LiteralPath $Exe
$v = $file.VersionInfo

Write-Host "== file =="
Write-Host ("path:      {0}" -f $file.FullName)
Write-Host ("size:      {0} bytes" -f $file.Length)
Write-Host ("sha256:    {0}" -f (Get-FileHash -Algorithm SHA256 -LiteralPath $file.FullName).Hash.ToLower())

Write-Host ""
Write-Host "== version resource (Properties -> Details) =="
$fields = [ordered]@{
    "FileVersion"      = $v.FileVersion
    "ProductVersion"   = $v.ProductVersion
    "ProductName"      = $v.ProductName
    "FileDescription"  = $v.FileDescription
    "CompanyName"      = $v.CompanyName
    "OriginalFilename" = $v.OriginalFilename
}
foreach ($k in $fields.Keys) {
    Write-Host ("{0,-18} {1}" -f ($k + ":"), $fields[$k])
}

Write-Host ""
Write-Host "== icon =="
Add-Type -AssemblyName System.Drawing
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($file.FullName)
if ($null -eq $icon) {
    Write-Warning "no icon resource could be extracted"
} else {
    $bmp = $icon.ToBitmap()
    $out = Join-Path $file.DirectoryName "exe-icon.png"
    $bmp.Save($out, [System.Drawing.Imaging.ImageFormat]::Png)
    Write-Host ("icon saved: {0} ({1}x{2})" -f $out, $bmp.Width, $bmp.Height)
    Write-Host "open it next to FlowDrop's blue rounded icon to compare."
    $bmp.Dispose()
    $icon.Dispose()
}

Write-Host ""
Write-Host "expected: FileVersion/ProductVersion 0.1.0.0, ProductName FlowDrop,"
Write-Host "          FileDescription 'FlowDrop 流转', CompanyName FlowDrop."
