@echo off
setlocal enabledelayedexpansion
set "ENVFILE=C:\ProgramData\wuling-runner\runner.env"
if exist "%ENVFILE%" (
  for /f "usebackq eol=# tokens=1,* delims==" %%a in ("%ENVFILE%") do set "%%a=%%b"
)
"%~dp0wuling-runner.exe"
