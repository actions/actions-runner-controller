#!/usr/bin/env bash
# We are not using `set -Eeuo pipefail` here because this file is sourced by
# other scripts that might not be ready for a strict Bash setup. The functions
# in this file do not require it, because they are not handling signals, have
# no external calls that can fail (printf as well as date failures are ignored),
# are not using any variables that need to be set, and are not using any pipes.

# This logger implementation can be replaced with another logger implementation
# by placing a script called `logger.bash` in `/usr/local/bin` of the image. The
# only requirement for the script is that it defines the following functions:
#
# - `log.debug`
# - `log.notice`
# - `log.warning`
# - `log.error`
# - `log.success`
#
# Each function **MUST** accept an arbitrary amount of arguments that make up
# the (unstructured) logging message.
#
# Additionally the following environment variables **SHOULD** be supported to
# disable their corresponding log entries, the value of the variables **MUST**
# not matter the mere fact that they are set is all that matters:
#
# - `LOG_DEBUG_DISABLED`
# - `LOG_NOTICE_DISABLED`
# - `LOG_WARNING_DISABLED`
# - `LOG_ERROR_DISABLED`
# - `LOG_SUCCESS_DISABLED`

# The log format is constructed in a way that it can easily be parsed with
# standard tools and simple string manipulations; pattern and example:
#
#     YYYY-MM-DD hh:mm:ss.SSS  $level --- $message
#     2022-03-19 10:01:23.172  NOTICE --- example message
#
# This function is an implementation detail and **MUST NOT** be called from
# outside this script (which is possible if the file is sourced).
__log() {
  local color instant level

  color=${1:?missing required <color> argument}
  shift

  level=${FUNCNAME[1]} # `main` if called from top-level
  level=${level#log.} # substring after `log.`
  level=${level^^} # UPPERCASE

  if [[ ! -v "LOG_${level}_DISABLED" ]]; then
    instant=$(date '+%F %T.%-3N' 2>/dev/null || :)

    # https://no-color.org/
    if [[ -v NO_COLOR ]]; then
      printf -- '%s  %s --- %s\n' "$instant" "$level" "$*" 1>&2 || :
    else
      printf -- '\033[0;%dm%s  %s --- %s\033[0m\n' "$color" "$instant" "$level" "$*" 1>&2 || :
    fi
  fi
}

# To log with a dynamic level use standard Bash capabilities:
#
#     level=notice
#     command || level=error
#     "log.$level" message
#
# @formatter:off
log.debug   () { __log 37 "$@"; } # white
log.notice  () { __log 34 "$@"; } # blue
log.warning () { __log 33 "$@"; } # yellow
log.error   () { __log 31 "$@"; } # red
log.success () { __log 32 "$@"; } # green
# @formatter:on
