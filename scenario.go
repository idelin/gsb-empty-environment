package netsec

// Setting indices for the terminal hardening scenario. The control host
// maps these bits to real configuration knobs on the managed host.
const (
	SettingTelnetUp       = iota // legacy plaintext entry is running
	SettingSSHUp                 // replacement encrypted entry is running
	SettingKeysDeployed          // SSH host/user keys are installed
	SettingTelnetPortOpen        // firewall passes the telnet port
	SettingSSHPortOpen           // firewall passes the SSH port
	SettingDebugShell            // debug shell exposed (dangerous entry)
	scenarioSettings
)

// TerminalHardeningProblem models the migration from a legacy telnet
// entry to an SSH entry. Initially telnet is up and reachable; the goal
// is SSH up and reachable with telnet fully retired. Two invariants
// hold in every state:
//
//   - management channel: telnet or SSH must remain usable, i.e. the
//     service is up and its firewall port is open;
//   - no dangerous entry: the debug shell is never exposed.
//
// The scenario includes a tempting but forbidden OpenDebugShell action;
// the planner never selects it because every resulting state violates
// the dangerous-entry invariant.
func TerminalHardeningProblem() *Problem {
	telnet, ssh := Bit(SettingTelnetUp), Bit(SettingSSHUp)
	keys := Bit(SettingKeysDeployed)
	telnetPort, sshPort := Bit(SettingTelnetPortOpen), Bit(SettingSSHPortOpen)
	debug := Bit(SettingDebugShell)

	mgmtChannel := func(s State) bool {
		telnetUsable := s&telnet != 0 && s&telnetPort != 0
		sshUsable := s&ssh != 0 && s&sshPort != 0
		return telnetUsable || sshUsable
	}

	return &Problem{
		NumSettings: int(scenarioSettings),
		Initial:     telnet | telnetPort,
		GoalAll:     ssh | keys | sshPort,
		GoalNone:    telnet | telnetPort | debug,
		Actions: []Action{
			{Name: "DeployKeys", Set: keys},
			{Name: "StartSSH", RequireAll: keys, Set: ssh},
			{Name: "OpenSSHPort", RequireAll: ssh, Set: sshPort},
			{Name: "CloseTelnetPort", RequireAll: ssh | sshPort, Clear: telnetPort},
			{Name: "StopTelnet", RequireNone: telnetPort, Clear: telnet},
			{Name: "OpenDebugShell", Set: debug},
		},
		Invariants: []Invariant{
			{Name: "management-channel", Hold: mgmtChannel},
			NeverAll("no-debug-shell", debug),
		},
	}
}
