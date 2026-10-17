clear
echo "Building Main App"
go build -o novabar.exe
echo "Building Safty Rescue CLI"
go build -o bin/shellctl.exe ./cmd/shellctl
echo "DONE"
