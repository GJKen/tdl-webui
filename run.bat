@echo off
cd /d "%~dp0"

start "" /min powershell -NoProfile -ExecutionPolicy Bypass -Command ^
  "for ($i=0; $i -lt 60; $i++) { try { $c = New-Object Net.Sockets.TcpClient; $c.Connect('127.0.0.1', 8080); $c.Close(); Start-Process 'http://127.0.0.1:8080'; exit } catch { Start-Sleep -Milliseconds 500 } }"

go run . server
pause