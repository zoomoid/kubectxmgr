package shell

// posixTemplate defines the bash/zsh wrapper around a quoted executable path.
// The response file carries only environment changes; stdout and exit status
// remain available to arbitrary commands, and no response is evaluated as code.
var posixTemplate = `switch() {
  local response_file result response
  response_file=$(mktemp "${TMPDIR:-/tmp}/kubectxmgr.XXXXXXXX") || return

  if %s --shell-result "$response_file" "$@"; then result=0; else result=$?; fi
  response=$(cat "$response_file")
  command rm -f -- "$response_file"
  [ "$result" -eq 0 ] || return "$result"

  case "$response" in
    "") ;;
    unset) unset KUBECONFIG ;;
    set:*)
      response=${response#set:}
      [ -f "$response" ] || { printf 'invalid kubeconfig response\n' >&2; return 1; }
      export KUBECONFIG="$response" ;;
    *) printf 'invalid switcher response\n' >&2; return 1 ;;
  esac
}
`

// fishTemplate implements the same response protocol with fish-native syntax.
// Its function is named kubeswitch because switch is a reserved fish keyword.
var fishTemplate = `function kubeswitch
  set -l tmp /tmp
  if set -q TMPDIR; set tmp "$TMPDIR"; end
  set -l response_file (mktemp "$tmp/kubectxmgr.XXXXXXXX")
  or return $status

  %s --shell-result "$response_file" $argv
  set -l result $status
  set -l response (cat "$response_file" | string collect)
  command rm -f -- "$response_file"
  test $result -eq 0; or return $result

  switch "$response"
    case ''
    case unset
      set -e KUBECONFIG
    case 'set:*'
      set -l path (string sub -s 5 -- "$response")
      test -f "$path"; or return 1
      set -gx KUBECONFIG "$path"
    case '*'
      printf 'invalid switcher response\n' >&2
      return 1
  end
end
`
