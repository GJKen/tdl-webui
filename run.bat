@echo off
cd /d "%~dp0"

rem 先杀掉仍占用 8080 端口的旧 server（按端口找 PID，兼容 go run 的临时 exe 名）
for /f "tokens=5" %%p in ('netstat -ano ^| findstr "127.0.0.1:8080" ^| findstr "LISTENING"') do (
  echo Killing old server on port 8080 ^(PID %%p^)...
  taskkill /F /PID %%p >nul 2>&1
)

start "" /min powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "for ($i=0; $i -lt 60; $i++) { try { $c = New-Object Net.Sockets.TcpClient; $c.Connect('127.0.0.1', 8080); $c.Close(); Start-Process 'http://127.0.0.1:8080'; exit } catch { Start-Sleep -Milliseconds 500 } }"

go run . server
pause