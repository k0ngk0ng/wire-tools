# bash completion for wirectl talk and wirectl-talk.
# Load with: source <(wirectl talk completion bash)

_wirectl_talk_completion() {
    local cur prev command_index command options
    COMPREPLY=()
    cur="${COMP_WORDS[COMP_CWORD]}"
    prev="${COMP_WORDS[COMP_CWORD-1]}"

    case "${COMP_WORDS[0]}" in
        wirectl)
            if (( COMP_CWORD == 1 )); then
                options="connect download talk update api version completion"
                COMPREPLY=( $(compgen -W "$options" -- "$cur") )
                return 0
            fi
            [[ "${COMP_WORDS[1]}" == "talk" ]] || return 0
            command_index=2
            ;;
        wirectl-talk)
            command_index=1
            ;;
        *)
            return 0
            ;;
    esac

    if (( COMP_CWORD == command_index )); then
        COMPREPLY=( $(compgen -W "call completion devices group init input invite join leave levels mute pair record status test unmute version volume watch" -- "$cur") )
        return 0
    fi

    # --state-dir is accepted before or after every talk command.
    if [[ "$prev" == "--state-dir" ]]; then
        COMPREPLY=( $(compgen -d -- "$cur") )
        return 0
    fi

    command="${COMP_WORDS[command_index]}"
    case "$command" in
        group)
            if (( COMP_CWORD == command_index + 1 )); then
                COMPREPLY=( $(compgen -W "list status watch levels create join invite use mute unmute rename mode leave" -- "$cur") )
                return 0
            fi
            if [[ "$prev" == --mode ]]; then COMPREPLY=( $(compgen -W "room direct" -- "$cur") ); return 0; fi
            case "$prev" in --code|--name|--listen) return 0 ;; esac
            COMPREPLY=( $(compgen -W "--json --name --mode --listen --code --state-dir --help" -- "$cur") )
            ;;
        call)
            COMPREPLY=( $(compgen -W "dial answer hangup button status --group --json --state-dir --help" -- "$cur") )
            ;;
        completion)
            if (( COMP_CWORD == command_index + 1 )); then
                COMPREPLY=( $(compgen -W "bash zsh" -- "$cur") )
            fi
            ;;
        record|input|test)
            if (( COMP_CWORD == command_index + 1 )); then
                case "$command" in
                    record) options="start stop status" ;;
                    input) options="start pause resume stop status" ;;
                    test) options="input output" ;;
                esac
                COMPREPLY=( $(compgen -W "$options" -- "$cur") )
                return 0
            fi
            if [[ "$prev" == --mode ]]; then
                COMPREPLY=( $(compgen -W "replace mix" -- "$cur") ); return 0
            fi
            case "$prev" in --peer|--device|--seconds|--group) return 0 ;; esac
            if [[ "${COMP_WORDS[command_index+1]}" == start ]] && (( COMP_CWORD == command_index + 2 )); then
                COMPREPLY=( $(compgen -f -- "$cur") ); return 0
            fi
            case "$command" in
                record) options="--peer --group --json --state-dir --help" ;;
                input) options="--mode --loop --group --json --state-dir --help" ;;
                test) options="--device --seconds --state-dir --help" ;;
            esac
            COMPREPLY=( $(compgen -W "$options" -- "$cur") )
            ;;
        devices)
            case "$prev" in --input|--output|--ring-output) return 0 ;; esac
            COMPREPLY=( $(compgen -W "set --json --input --output --ring-output --state-dir --help" -- "$cur") )
            ;;
        init)
            if [[ "$prev" == --mode ]]; then COMPREPLY=( $(compgen -W "room direct" -- "$cur") ); return 0; fi
            case "$prev" in
                --key-file)
                    COMPREPLY=( $(compgen -f -- "$cur") )
                    return 0
                    ;;
            esac
            options="--mode --listen --input --output --ring-output --headphones --key-file --peers --state-dir --help"
            COMPREPLY=( $(compgen -W "$options" -- "$cur") )
            ;;
        pair|join)
            case "$prev" in
                --code|--listen|--input|--output|--ring-output|--peers)
                    return 0
                    ;;
            esac
            options="--code --listen --input --output --ring-output --headphones --state-dir --help"
            COMPREPLY=( $(compgen -W "$options" -- "$cur") )
            ;;
        status|watch)
            COMPREPLY=( $(compgen -W "--json --state-dir --help" -- "$cur") )
            ;;
        volume)
            COMPREPLY=( $(compgen -W "output peer --group --json --state-dir --help" -- "$cur") )
            ;;
        levels)
            COMPREPLY=( $(compgen -W "--state-dir --help" -- "$cur") )
            ;;
        mute|unmute)
            COMPREPLY=( $(compgen -W "input output --state-dir --help" -- "$cur") )
            ;;
        invite|leave|version)
            COMPREPLY=( $(compgen -W "--state-dir --help" -- "$cur") )
            ;;
    esac
}

complete -o bashdefault -o default -F _wirectl_talk_completion wirectl wirectl-talk
