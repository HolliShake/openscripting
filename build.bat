@echo off
go build -gcflags="-m=2" ./main > build.txt 2>&1
exit /b %ERRORLEVEL%
