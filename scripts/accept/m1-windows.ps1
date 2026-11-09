param(
    [Parameter(Mandatory=$true,Position=0)][string]$Video,
    [ValidateSet('mf','mpv')][string]$Backend='mf',
    [string]$Mpv='',
    [int]$Duration=1800,
    [string]$Output=(Join-Path $env:TEMP ('wallpaper-m1-'+[guid]::NewGuid().ToString('N')))
)
# Real desktop only. Keep unrelated windows minimized and use a moving clip.
$ErrorActionPreference='Stop'
Set-Location (Join-Path $PSScriptRoot '../..')
$Video=(Resolve-Path $Video).Path
New-Item -ItemType Directory -Path $Output -Force | Out-Null
Start-Transcript -Path (Join-Path $Output 'results.txt') | Out-Null
$process=$null
$failed=$false
$script:children=@{}
Add-Type -AssemblyName System.Drawing
Add-Type -AssemblyName System.Windows.Forms
Add-Type @'
using System;
using System.Text;
using System.Runtime.InteropServices;
public class M1Native {
 [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)]
 public struct STARTUPINFO { public int cb; public string reserved,desktop,title; public int x,y,xsize,ysize,xchars,ychars,fill,flags; public short show,reserved2; public IntPtr reservedPtr,input,output,error; }
 [StructLayout(LayoutKind.Sequential)] public struct PROCESS_INFORMATION {public IntPtr process,thread;public uint pid,tid;}
 [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool CreateProcess(string app,StringBuilder command,IntPtr pa,IntPtr ta,bool inherit,uint flags,IntPtr env,string cwd,ref STARTUPINFO si,out PROCESS_INFORMATION pi);
 [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
 [DllImport("kernel32.dll",SetLastError=true)] public static extern bool GenerateConsoleCtrlEvent(uint kind,uint group);
 [DllImport("kernel32.dll")] static extern IntPtr GetConsoleWindow();
 [DllImport("kernel32.dll")] static extern bool AllocConsole();
 public static int Start(string app,string args) {
  if(GetConsoleWindow()==IntPtr.Zero && !AllocConsole()) throw new Exception("BLOCKED: no console for graceful CTRL_BREAK delivery");
  STARTUPINFO si=new STARTUPINFO();si.cb=Marshal.SizeOf(si);PROCESS_INFORMATION pi;
  if(!CreateProcess(app,new StringBuilder("\""+app+"\" "+args),IntPtr.Zero,IntPtr.Zero,false,0x200,IntPtr.Zero,null,ref si,out pi)) throw new System.ComponentModel.Win32Exception();
  CloseHandle(pi.process);CloseHandle(pi.thread);return (int)pi.pid;
 }
}
'@
function Read-State {
    if (Test-Path $status) { return (Get-Content -Raw $status | ConvertFrom-Json) }
    return $null
}
function Remember-Children([int]$ParentID) {
    foreach($child in @(Get-CimInstance Win32_Process -Filter "ParentProcessId=$ParentID")) {
        $script:children[[int]$child.ProcessId]=$child.CreationDate
    }
}
function Assert-NoChildren {
    foreach($childID in @($script:children.Keys)) {
        $current=Get-CimInstance Win32_Process -Filter "ProcessId=$childID"
        if($current -and $current.CreationDate -eq $script:children[$childID]) { throw "FAIL M1-07 residual child $childID" }
    }
}
function Start-Wallpaper([string]$Path) {
    if(Test-Path $status){Remove-Item $status}
    $arguments='-play "'+$Path+'" -backend '+$Backend+' -status "'+$status+'"'
    if($Mpv){$arguments+=' -mpv "'+$Mpv+'"'}
    $nativeID=[M1Native]::Start($exe,$arguments)
    return [System.Diagnostics.Process]::GetProcessById($nativeID)
}
function Capture([string]$Path) {
    $bounds=[System.Windows.Forms.Screen]::PrimaryScreen.Bounds
    $bitmap=New-Object System.Drawing.Bitmap($bounds.Width,$bounds.Height)
    $graphics=[System.Drawing.Graphics]::FromImage($bitmap)
    try {
        $graphics.CopyFromScreen($bounds.Location,[System.Drawing.Point]::Empty,$bounds.Size)
        $bitmap.Save($Path,[System.Drawing.Imaging.ImageFormat]::Png)
    } finally {$graphics.Dispose();$bitmap.Dispose()}
}
try {
    git rev-parse HEAD | Set-Content (Join-Path $Output 'commit.txt')
    Get-ComputerInfo | Out-File (Join-Path $Output 'system.txt')
    Get-CimInstance Win32_VideoController | Format-List * | Out-File (Join-Path $Output 'display.txt')
    Get-FileHash $Video | Format-List | Out-File (Join-Path $Output 'media.txt')
    $exe=Join-Path $Output 'wallpaperd.exe'
    $probe=Join-Path $Output 'probe.exe'
    $env:CGO_ENABLED='0'
    # Console subsystem intentionally permits real CTRL_BREAK / os.Interrupt.
    go build -o $exe ./cmd/wallpaperd
    if($LASTEXITCODE){throw 'FAIL build wallpaperd'}
    go build -o $probe ./scripts/accept/probe
    if($LASTEXITCODE){throw 'FAIL build probe'}
    $status=Join-Path $Output 'status.json'
    $process=Start-Wallpaper $Video
    $deadline=[DateTime]::UtcNow.AddSeconds(25)
    $startupIndex=0
    do {
        $state=Read-State
        if($state){Copy-Item $status (Join-Path $Output "startup-$startupIndex.json");$startupIndex++}
        Remember-Children $process.Id
        if($state -and $state.phase -eq 'confirmed'){break}
        if($process.HasExited -or ($state -and $state.phase -in @('failed','stopped'))){throw 'FAIL M1-04 startup failed'}
        Start-Sleep -Milliseconds 100
    } while([DateTime]::UtcNow -lt $deadline)
    if(!$state -or $state.phase -ne 'confirmed' -or $state.pid -ne $process.Id){throw 'FAIL M1-04 confirmation timeout or wrong process'}
    Write-Output 'PASS M1-04 confirmed from platform evidence'
    $window=$state.window
    Copy-Item $status (Join-Path $Output 'confirmed.json')
    $start=[DateTime]::UtcNow
    $samples=@();$index=0;$capture=$true;$motionFailed=$false
    while(([DateTime]::UtcNow-$start).TotalSeconds -lt $Duration) {
        $process.Refresh();$state=Read-State
        if($process.HasExited -or $state.phase -ne 'confirmed'){throw 'FAIL M1-02 playback evidence lost'}
        Remember-Children $process.Id
        Copy-Item $status (Join-Path $Output "status-$index.json")
        $all=@($process)
        foreach($childID in @($script:children.Keys)) {$child=Get-Process -Id $childID -ErrorAction SilentlyContinue;if($child){$all+=$child}}
        $rss=($all | Measure-Object WorkingSet64 -Sum).Sum
        $handles=($all | Measure-Object HandleCount -Sum).Sum
        $sample=[pscustomobject]@{timestamp=[DateTime]::UtcNow.ToString('o');rss_bytes=$rss;handles=$handles;child_count=$all.Count-1}
        $samples+=$sample
        $sample | Export-Csv -NoTypeInformation -Append (Join-Path $Output 'resources.csv')
        if($capture){
            $image=Join-Path $Output "frame-$index.png"
            try {Capture $image} catch {Write-Output "BLOCKED M1-02 desktop capture: $_";$capture=$false}
            if($capture -and $index -gt 0){
                $previous=Join-Path $Output "frame-$($index-1).png"
                $diff=& $probe $previous $image 2>&1
                $diff | Out-File (Join-Path $Output "diff-$index.txt")
                if($LASTEXITCODE){
                    if("$diff" -match 'BLOCKED'){$capture=$false;Write-Output 'BLOCKED M1-02 desktop pixels unavailable'}
                    else{$motionFailed=$true;$failed=$true;Write-Output 'FAIL M1-02 screenshot did not change'}
                }
            }
        }
        $index++
        Start-Sleep -Seconds 10 # Real-device observation interval.
    }
    if($capture -and !$motionFailed -and $Duration -ge 600){Write-Output 'PASS M1-02 ten-minute desktop motion samples'}
    if($Duration -lt 600){Write-Output 'BLOCKED M1-02 shortened observation'}
    if($Duration -ge 1800 -and $samples.Count -ge 10){
        $n=[Math]::Max(1,[int]($samples.Count/5))
        $first=$samples | Select-Object -First $n
        $last=$samples | Select-Object -Last $n
        $r0=($first | Measure-Object rss_bytes -Average).Average;$r1=($last | Measure-Object rss_bytes -Average).Average
        $h0=($first | Measure-Object handles -Average).Average;$h1=($last | Measure-Object handles -Average).Average
        if(($r1-$r0) -gt [Math]::Max(50MB,$r0*.2) -or ($h1-$h0) -gt [Math]::Max(50,$h0*.1)){$failed=$true;Write-Output 'FAIL M1-08 resource growth exceeded RSS 50 MiB/20% or handles 50/10% threshold'}
        else{Write-Output 'PASS M1-08 sampled parent + child RSS/handle trend; raw CSV retained'}
    }else{Write-Output 'BLOCKED M1-08 requires 30 minutes'}
    $watch=[Diagnostics.Stopwatch]::StartNew()
    if(![M1Native]::GenerateConsoleCtrlEvent(1,[uint32]$process.Id)){throw 'BLOCKED M1-07 could not deliver graceful CTRL_BREAK'}
    if(!$process.WaitForExit(5000)){throw 'FAIL M1-07 exit exceeded 5 seconds'}
    $watch.Stop()
    if($process.ExitCode -ne 0){throw 'FAIL M1-07 abnormal exit'}
    Assert-NoChildren
    & $probe -window $window
    if($LASTEXITCODE){throw 'FAIL M1-07 native surface remains'}
    Write-Output "PASS M1-07 graceful interrupt exit, no child/surface residue ($($watch.ElapsedMilliseconds) ms)"
    $process=$null
    $bad=Join-Path $Output 'corrupt.mp4';'invalid media' | Set-Content $bad
    $process=Start-Wallpaper $bad
    $deadline=[DateTime]::UtcNow.AddSeconds(25)
    while(!$process.HasExited -and [DateTime]::UtcNow -lt $deadline){Remember-Children $process.Id;Start-Sleep -Milliseconds 100}
    if(!$process.HasExited){throw 'FAIL M1-05 corrupt media process remained'}
    $state=Read-State
    if($process.ExitCode -ne 1 -or !$state.reason -or $state.visible -ne 'no'){throw 'FAIL M1-05 missing rejection or cleanup evidence'}
    Assert-NoChildren
    Write-Output 'PASS M1-05 corrupt media rejected and cleaned'
    $process=$null
    Write-Output 'BLOCKED M1-01/M1-03 require recording review of first frame and loop seams'
    Write-Output 'BLOCKED M1-06/M1-07 icon interaction and restored desktop require human confirmation'
    Write-Output 'BLOCKED M1-09 compare both backend artifact sets and record human quality decision'
} catch {
    Write-Output $_
    exit 1
} finally {
    if($process -and !$process.HasExited){
        [void][M1Native]::GenerateConsoleCtrlEvent(1,[uint32]$process.Id)
        if(!$process.WaitForExit(5000)){$process.Kill()}
    }
    Write-Output "Artifacts: $Output"
    Stop-Transcript | Out-Null
}

if($failed){exit 1}
