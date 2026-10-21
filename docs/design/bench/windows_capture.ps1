<# 动态壁纸 Windows 采集脚本（草稿，未在真机运行过）
用法: .\windows_capture.ps1 -Phase baseline|wallpaper -Seconds 120 [-PresentMon C:\tools\PresentMon.exe] [-MpvPipe \\.\pipe\mpvwp]
输出: .\out\<phase>_<时间>.csv（进程 CPU%、私有字节、GPU 引擎利用率）+ mpv 状态 json #>
param([ValidateSet('baseline','wallpaper')][string]$Phase='baseline',[int]$Seconds=120,
      [string[]]$Procs=@('mpv','msedgewebview2','wpproto_windows','dwm','explorer'),
      [string]$PresentMon='',[string]$MpvPipe='')
$ErrorActionPreference='Stop'; New-Item -ItemType Directory -Force out | Out-Null
$ts=Get-Date -Format yyyyMMdd_HHmmss; $csv="out\${Phase}_$ts.csv"
$ctr=@('\Processor(_Total)\% Processor Time')
foreach($p in $Procs){ $ctr+="\Process($p*)\% Processor Time"; $ctr+="\Process($p*)\Private Bytes" }
$ctr+='\GPU Engine(*engtype_3D)\Utilization Percentage'
$ctr+='\GPU Engine(*engtype_VideoDecode)\Utilization Percentage'
$ctr+='\GPU Process Memory(*)\Dedicated Usage'
if($PresentMon){ $pm=Start-Process $PresentMon -ArgumentList "--output_file out\pm_${Phase}_$ts.csv --timed $Seconds --terminate_after_timed" -PassThru -WindowStyle Hidden }
Get-Counter -Counter $ctr -SampleInterval 1 -MaxSamples $Seconds -ErrorAction SilentlyContinue |
  ForEach-Object { foreach($s in $_.CounterSamples){ [pscustomobject]@{t=$_.Timestamp.ToString('o');path=$s.Path;value=[math]::Round($s.CookedValue,3)} } } |
  Export-Csv $csv -NoTypeInformation
# 注: Process % 是“单核百分比”，除以 100 = 核数；GPU Engine 需按 pid_ 前缀聚合到进程
if($MpvPipe){ try{
  $c=New-Object IO.Pipes.NamedPipeClientStream('.',($MpvPipe -replace '^\\\\\.\\pipe\\',''),'InOut'); $c.Connect(2000)
  $w=New-Object IO.StreamWriter($c); $r=New-Object IO.StreamReader($c); $w.AutoFlush=$true; $o=@{}
  foreach($prop in 'hwdec-current','frame-drop-count','decoder-frame-drop-count','video-codec','estimated-vf-fps'){
    $w.WriteLine((@{command=@('get_property',$prop)}|ConvertTo-Json -Compress)); $o[$prop]=($r.ReadLine()|ConvertFrom-Json).data }
  $o|ConvertTo-Json|Set-Content "out\mpv_${Phase}_$ts.json"; $c.Dispose() } catch { Write-Warning "mpv IPC 失败: $_" } }
if($pm){ $pm.WaitForExit() }
# 汇总：每个计数器路径的均值/最大值
Import-Csv $csv | Group-Object path | ForEach-Object { $v=$_.Group|ForEach-Object{[double]$_.value}
  [pscustomobject]@{path=$_.Name;avg=[math]::Round(($v|Measure-Object -Average).Average,3);max=($v|Measure-Object -Maximum).Maximum} } |
  Export-Csv "out\summary_${Phase}_$ts.csv" -NoTypeInformation
Write-Host "完成: $csv"
