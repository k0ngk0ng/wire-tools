#compdef wirectl wirectl-talk

# zsh completion for wirectl talk and wirectl-talk.
# Load after enabling zsh completion with: autoload -Uz compinit && compinit
# Then load with: source <(wirectl talk completion zsh)

_wirectl_talk_completion() {
    local talk_index command_index command
    local -a candidates root_commands

    case "$words[1]" in
        wirectl)
            if (( CURRENT == 2 )); then
                root_commands=(connect download talk update api version completion)
                _describe 'command' root_commands
                return
            fi
            [[ "$words[2]" == talk ]] || return
            talk_index=2
            ;;
        wirectl-talk)
            talk_index=1
            ;;
        *)
            return
            ;;
    esac

    command_index=$((talk_index + 1))
    if (( CURRENT == command_index )); then
        candidates=(
            'call:dial, answer or hang up a direct call'
            'completion:print bash or zsh completion script'
            'devices:list audio devices'
            'group:manage rooms and watch members'
            'init:create room config'
            'input:send an audio file to peers'
            'levels:watch microphone and speaker audio levels'
            'record:record all peers or a selected peer'
            'test:test a local audio device with a live meter'
            'invite:generate a one-use pairing code'
            'join:join a room or connect the saved room'
            'leave:disconnect the local audio session'
            'mute:mute local input or output'
            'pair:save a room without starting audio'
            'status:show session status'
            'unmute:unmute local input or output'
            'version:print version'
            'watch:watch session status'
            'volume:adjust output or member gain'
        )
        _describe 'talk command' candidates
        return
    fi

    command="$words[$command_index]"
    case "$command" in
        group)
            if (( CURRENT == command_index + 1 )); then
                candidates=(list status watch levels create join invite use mute unmute rename mode leave)
                _describe 'room action' candidates
                return
            fi
            _arguments -s \
                '--json[output JSON]' \
                '--mode[connection mode for create]:mode:(room direct)' \
                '--name[local room name]:name:' \
                '--listen[room listen address]:address:' \
                '--code[temporary pairing code]:code:' \
                '--state-dir[configuration directory]:directory:_files -/' \
                '1:room ID, name or inviter address:' \
                '2:new room name:'
            ;;
        call)
            _arguments -s \
                '--json[output JSON]' \
                '--group[target connection]:room:' \
                '--state-dir[configuration directory]:directory:_files -/' \
                '1:action:(dial answer hangup button status)'
            ;;
        completion)
            if (( CURRENT == command_index + 1 )); then
                candidates=(bash zsh)
                _describe 'shell' candidates
            else
                _message 'shell (bash or zsh)'
            fi
            ;;
        record|input|test)
            if (( CURRENT == command_index + 1 )); then
                case "$command" in
                    record) candidates=(start stop status) ;;
                    input) candidates=(start pause resume stop status) ;;
                    test) candidates=(input output) ;;
                esac
                _describe 'action' candidates
                return
            fi
            if [[ "$words[$((command_index+1))]" == start ]] && (( CURRENT == command_index + 2 )); then
                _files
                return
            fi
            _arguments -s \
                '--mode[file input mode]:mode:(replace mix)' \
                '--loop[repeat file input]' \
                '--peer[record one peer]:ID or address:' \
                '--group[target room]:room ID or name:' \
                '--device[test a specific device]:device ID:' \
                '--seconds[test duration]:seconds:' \
                '--json[output JSON]' \
                '--state-dir[configuration directory]:directory:_files -/'
            ;;
        devices)
            _arguments -s \
                '--json[output JSON for scripts]' \
                '--input[microphone device ID]:device ID:' \
                '--output[call output device ID]:device ID:' \
                '--ring-output[incoming ringtone device ID]:device ID:' \
                '--state-dir[configuration directory]:directory:_files -/' \
                '1:action:(set)'
            ;;
        init)
            _arguments -s \
                '--mode[connection mode]:mode:(room direct)' \
                '--listen[UDP listen address]:address:' \
                '--input[input device ID]:device ID:' \
                '--output[output device ID]:device ID:' \
                '--ring-output[incoming ringtone device ID]:device ID:' \
                '--headphones[legacy compatibility option]' \
                '--key-file[read a room key from a private file]:file:_files' \
                '--peers[comma-separated reachable UDP addresses]:addresses:' \
                '--state-dir[configuration directory]:directory:_files -/'
            ;;
        pair|join)
            _arguments -s \
                '--code[temporary six-digit invitation code]:code:' \
                '--listen[local UDP listen address]:address:' \
                '--input[input device ID]:device ID:' \
                '--output[output device ID]:device ID:' \
                '--ring-output[incoming ringtone device ID]:device ID:' \
                '--headphones[legacy compatibility option]' \
                '--state-dir[configuration directory]:directory:_files -/' \
                '1:inviter host and port:'
            ;;
        status|watch)
            _arguments -s \
                '--json[output JSON for scripts]' \
                '--state-dir[configuration directory]:directory:_files -/'
            ;;
        volume)
            _arguments -s \
                '--json[output JSON]' \
                '--group[target room]:room:' \
                '--state-dir[configuration directory]:directory:_files -/' \
                '1:target:(output peer)' \
                '*:gain or member address:'
            ;;
        levels)
            _arguments -s \
                '--state-dir[configuration directory]:directory:_files -/'
            ;;
        mute|unmute)
            _arguments -s \
                '--state-dir[configuration directory]:directory:_files -/' \
                '1:audio direction:(input output)'
            ;;
        invite|leave|version)
            _arguments -s \
                '--state-dir[configuration directory]:directory:_files -/'
            ;;
    esac
}

# Autoloaded completion files must run on the first Tab as well as later calls.
# When sourced manually, only register the function.
if [[ $ZSH_EVAL_CONTEXT == *:file ]]; then
    if (( $+functions[compdef] )); then
        compdef _wirectl_talk_completion wirectl wirectl-talk
    fi
else
    _wirectl_talk_completion "$@"
fi
