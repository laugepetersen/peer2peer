# peer2peer

## How to start?
Run following command in each terminal
```sh
go run main.go OWNPORT PEER1 PEER2 ...
```
Hit enter to ask for critical section.
Critical section is held for 5 seconds.

## Example
Run each command in a seperate terminal
```sh
go run main.go 5000 5001 5002
go run main.go 5001 5000 5002
go run main.go 5002 5000 5001
enter
enter
enter
```
