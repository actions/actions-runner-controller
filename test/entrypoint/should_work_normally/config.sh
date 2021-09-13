#!/bin/bash

export LIGHTGREEN='\e[0;32m'
export LIGHTRED='\e[0;31m'
export WHITE='\e[0;97m'
export RESET='\e[0m'

log(){
  printf "\t${WHITE}$@${RESET}\n" 2>&1
}

success(){
  printf "\t${LIGHTGREEN}$@${RESET}\n" 2>&1
}

error(){
  printf "\t${LIGHTRED}$@${RESET}\n" 2>&1
}

success "I'm configured normally"
touch .runner
echo "$*" > runner_config
success "created a dummy config file"
success
# Adding a counter to see how many times we've gone through the configuration step
count=`cat counter 2>/dev/null|| echo "0"`
count=$((count + 1))
echo ${count} > counter

