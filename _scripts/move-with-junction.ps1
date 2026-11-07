# 迁移目录到另一个盘，并在原位置留同名 junction（程序仍按原路径访问）。
#
# 用法:
#   toolbox_run { tool: "move-with-junction", args: ["D:\\Tools\\big", "E:\\moved\\big"] }
#   加 --dry-run 只检查不搬；加 --force 跳过占用检查（危险）
#
# 步骤: 占用检查 -> robocopy 复制 -> 校验 -> 删源 -> mklink /J -> 读验证
param(
  [Parameter(Mandatory = $true)][string]$Source,
  [Parameter(Mandatory = $true)][string]$Target,
  [switch]$DryRun,
  [switch]$Force
)

$ErrorActionPreference = 'Stop'

function Say($msg) { Write-Host $msg }

if (-not (Test-Path -LiteralPath $Source -PathType Container)) { throw "源目录不存在: $Source" }
if (Test-Path -LiteralPath $Target) { throw "目标已存在，先确认: $Target" }
$sourceFull = (Resolve-Path -LiteralPath $Source).Path
$targetParent = Split-Path -Parent $Target
if (-not (Test-Path -LiteralPath $targetParent)) { New-Item -ItemType Directory -Path $targetParent -Force | Out-Null }

# 1) 占用检查：优先用 Sysinternals handle64/handle，没有就退化为"尝试独占重命名"
$busy = @()
if (-not $Force) {
  $handle = Get-Command handle64.exe, handle.exe -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($handle) {
    Say "检查占用: $($handle.Source)"
    $out = & $handle.Source -nobanner -accepteula $sourceFull 2>$null
    $busy = $out | Where-Object { $_ -match [regex]::Escape($sourceFull) -and $_ -notmatch 'pid:\s*0\b' }
  } else {
    Say '没找到 handle.exe，改用独占重命名探测（不够精确）'
    $probe = $sourceFull + '.__probe__'
    try {
      Rename-Item -LiteralPath $sourceFull -NewName (Split-Path -Leaf $probe) -ErrorAction Stop
      Rename-Item -LiteralPath $probe -NewName (Split-Path -Leaf $sourceFull) -ErrorAction Stop
    } catch { $busy = @("重命名探测失败：可能有程序正在使用该目录") }
  }
}

if ($busy.Count -gt 0) {
  Say "有进程占用该目录，已中止："
  $busy | Select-Object -First 20 | ForEach-Object { Say "  $_" }
  Say "关掉这些程序后重试；确实要硬搬就加 --force"
  exit 3
}

if ($DryRun) {
  Say "[dry-run] 会执行：robocopy `"$sourceFull`" `"$Target`" /E /MOVE /R:2 /W:2"
  Say "[dry-run] 然后：mklink /J `"$sourceFull`" `"$Target`""
  exit 0
}

# 2) 迁移：robocopy /MOVE 会在成功复制后删源
Say "开始迁移（robocopy /E /MOVE）…"
$rc = Start-Process -FilePath robocopy.exe -ArgumentList @("`"$sourceFull`"", "`"$Target`"", '/E', '/MOVE', '/R:2', '/W:2', '/NFL', '/NDL', '/NJH', '/NJS') -Wait -PassThru -NoNewWindow
if ($rc.ExitCode -ge 8) { throw "robocopy 失败，退出码 $($rc.ExitCode)" }

# 3) 原位置建 junction（不需要管理员权限）
Say "建立 junction…"
cmd /c mklink /J "`"$sourceFull`"" "`"$Target`"" | Out-Null
if (-not (Test-Path -LiteralPath $sourceFull)) { throw "junction 创建失败" }

# 4) 验证：通过原路径读目标内容
$firstItem = Get-ChildItem -LiteralPath $sourceFull -ErrorAction SilentlyContinue | Select-Object -First 1
Say "完成：$sourceFull -> $Target"
Say "验证读取: $(if ($firstItem) { $firstItem.Name } else { '(空目录)' })"
Say "回滚命令: rmdir `"$sourceFull`" && move `"$Target`" `"$sourceFull`""
