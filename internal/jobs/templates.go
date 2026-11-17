package jobs

// systemdServiceTemplate defines a short-lived discovery process with a private
// file-creation mask. Environment values and argv are escaped before formatting.
var systemdServiceTemplate = `# %s
[Unit]
Description=Refresh kubectxmgr discovery snapshots

[Service]
Type=oneshot
UMask=0077
Environment=%s
Environment=%s
ExecStart=%s
`

// systemdEnvironmentTemplate appends one escaped non-secret environment assignment.
var systemdEnvironmentTemplate = "Environment=%s\n"

// systemdCalendarTimerTemplate schedules sub-minute refreshes with persistent
// catch-up. The numeric placeholder is an interval in whole seconds.
var systemdCalendarTimerTemplate = `# %s
[Unit]
Description=Schedule kubectxmgr discovery

[Timer]
OnCalendar=*-*-* *:*:00/%d
Persistent=true
AccuracySec=1s
Unit=%s.service

[Install]
WantedBy=timers.target
`

// systemdIntervalTimerTemplate supports arbitrary minute-or-longer intervals.
// Startup and hourly persistent triggers cover runs missed while the user manager
// was unavailable; discover --due decides whether a store actually needs a scan.
var systemdIntervalTimerTemplate = `# %s
[Unit]
Description=Schedule kubectxmgr discovery

[Timer]
OnStartupSec=10s
OnUnitInactiveSec=%ds
OnCalendar=hourly
Persistent=true
AccuracySec=1s
Unit=%s.service

[Install]
WantedBy=timers.target
`

// launchAgentTemplate defines a per-user launchd agent with literal argv,
// an allowlisted environment, and separate protected stdout/stderr log paths.
// Umask is decimal 63, equivalent to octal 0077.
var launchAgentTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- %s -->
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>%s</array>
<key>RunAtLoad</key><true/>
<key>StartInterval</key><integer>%d</integer>
<key>Umask</key><integer>63</integer>
<key>EnvironmentVariables</key><dict><key>PATH</key><string>%s</string><key>KUBECONFIG</key><string>%s</string>%s</dict>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`

// launchArgumentTemplate wraps one XML-escaped argument without shell parsing.
var launchArgumentTemplate = "<string>%s</string>"

// launchEnvironmentTemplate renders an allowlisted name and XML-escaped value.
var launchEnvironmentTemplate = "<key>%s</key><string>%s</string>"
