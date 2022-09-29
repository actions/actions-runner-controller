#!/usr/bin/env bash

export LIGHTGREEN='\e[0;32m'
export LIGHTRED='\e[0;31m'
export WHITE='\e[0;97m'
export RESET='\e[0m'

log(){
  printf "${WHITE}$@${RESET}\n" 2>&1
}

success(){
  printf "${LIGHTGREEN}$@${RESET}\n" 2>&1
}

error(){
  printf "${LIGHTRED}$@${RESET}\n" 2>&1
}
