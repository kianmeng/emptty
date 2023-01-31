package src

import (
	"os"
	"os/exec"
	"strings"
)

const (
	envXdgConfigHome   = "XDG_CONFIG_HOME"
	envXdgRuntimeDir   = "XDG_RUNTIME_DIR"
	envXdgSessionId    = "XDG_SESSION_ID"
	envXdgSessionType  = "XDG_SESSION_TYPE"
	envXdgSessionClass = "XDG_SESSION_CLASS"
	envXdgSeat         = "XDG_SEAT"
	envHome            = "HOME"
	envPwd             = "PWD"
	envUser            = "USER"
	envLogname         = "LOGNAME"
	envXauthority      = "XAUTHORITY"
	envDisplay         = "DISPLAY"
	envShell           = "SHELL"
	envLang            = "LANG"
	envPath            = "PATH"
	envDesktopSession  = "DESKTOP_SESSION"
	envXdgSessDesktop  = "XDG_SESSION_DESKTOP"
	envUid             = "UID"
)

var interrupted bool

// session defines basic functions expected from desktop session
type session interface {
	startCarrier()
	getCarrierPid() int
	finishCarrier() error
}

// commonSession defines structure with data required for starting the session
type commonSession struct {
	session
	usr  *sysuser
	d    *desktop
	conf *config
	dbus *dbus
}

// Starts user's session
func startSession(usr *sysuser, d *desktop, conf *config) {
	s := &commonSession{nil, usr, d, conf, nil}

	switch d.env {
	case Wayland:
		s.session = &waylandSession{s}
	case Xorg:
		s.session = &xorgSession{s, nil}
	}

	s.start()
}

// Performs common start of session
func (s *commonSession) start() {
	s.defineEnvironment()

	s.startCarrier()

	if !s.conf.NoXdgFallback {
		s.usr.setenv(envXdgSessionType, s.d.env.sessionType())
	}

	if s.conf.AlwaysDbusLaunch {
		s.dbus = &dbus{}
	}

	session, strExec := s.prepareGuiCommand()
	go handleInterrupt(makeInterruptChannel(), session)

	sessionErrLog, sessionErrLogErr := initSessionErrorLogger(s.conf)
	if sessionErrLogErr == nil {
		session.Stderr = sessionErrLog
		defer sessionErrLog.Close()
	} else {
		logPrint(sessionErrLogErr)
	}

	if s.dbus != nil {
		s.dbus.launch(s.usr)
	}

	logPrint("Starting " + strExec)
	session.Env = s.usr.environ()
	if err := session.Start(); err != nil {
		s.finishCarrier()
		handleErr(err)
	}

	pid := s.getCarrierPid()
	if pid <= 0 {
		pid = session.Process.Pid
	}

	utmpEntry := addUtmpEntry(s.usr.username, pid, s.conf.strTTY(), s.usr.getenv(envDisplay))
	logPrint("Added utmp entry")

	err := session.Wait()

	if s.dbus != nil {
		s.dbus.interrupt()
	}

	carrierErr := s.finishCarrier()

	endUtmpEntry(utmpEntry)
	logPrint("Ended utmp entry")

	if !interrupted && err != nil {
		logPrint(strExec + " finished with error: " + err.Error() + ". For more details see `SESSION_ERROR_LOGGING` in configuration.")
		handleStrErr(s.d.env.string() + " session finished with error, please check logs")
	}

	if !interrupted && carrierErr != nil {
		logPrint(s.d.env.string() + " finished with error: " + carrierErr.Error())
		handleStrErr(s.d.env.string() + " finished with error, please check logs")
	}
}

// Prepares environment and env variables for authorized user.
func (s *commonSession) defineEnvironment() {
	defineSpecificEnvVariables(s.usr)

	s.usr.setenv(envHome, s.usr.homedir)
	s.usr.setenv(envPwd, s.usr.homedir)
	s.usr.setenv(envUser, s.usr.username)
	s.usr.setenv(envLogname, s.usr.username)
	s.usr.setenv(envUid, s.usr.strUid())
	if !s.conf.NoXdgFallback {
		s.usr.setenvIfEmpty(envXdgConfigHome, s.usr.homedir+"/.config")
		s.usr.setenvIfEmpty(envXdgRuntimeDir, "/run/user/"+s.usr.strUid())
		s.usr.setenvIfEmpty(envXdgSeat, "seat0")
		s.usr.setenv(envXdgSessionClass, "user")
	}
	s.usr.setenv(envShell, s.usr.getShell())
	s.usr.setenvIfEmpty(envLang, s.conf.Lang)
	s.usr.setenvIfEmpty(envPath, os.Getenv(envPath))

	if !s.conf.NoXdgFallback {
		if s.d.name != "" {
			s.usr.setenv(envDesktopSession, s.d.name)
			s.usr.setenv(envXdgSessDesktop, s.d.name)
		} else if s.d.child != nil && s.d.child.name != "" {
			s.usr.setenv(envDesktopSession, s.d.child.name)
			s.usr.setenv(envXdgSessDesktop, s.d.child.name)
		}
	}

	logPrint("Defined Environment")

	// create XDG folder
	if !s.conf.NoXdgFallback {
		if !fileExists(s.usr.getenv(envXdgRuntimeDir)) {
			err := os.MkdirAll(s.usr.getenv(envXdgRuntimeDir), 0700)
			handleErr(err)

			// Set owner of XDG folder
			os.Chown(s.usr.getenv(envXdgRuntimeDir), s.usr.uid, s.usr.gid)

			logPrint("Created XDG folder")
		} else {
			logPrint("XDG folder already exists, no need to create")
		}
	}

	os.Chdir(s.usr.getenv(envPwd))
}

// Prepares command for starting GUI.
func (s *commonSession) prepareGuiCommand() (cmd *exec.Cmd, strExec string) {
	strExec, allowStartupPrefix := s.d.getStrExec()

	startScript := s.d.isUser && !allowStartupPrefix

	if allowStartupPrefix && s.conf.XinitrcLaunch && s.d.env == Xorg && !strings.Contains(strExec, ".xinitrc") && fileExists(s.usr.homedir+"/.xinitrc") {
		startScript = true
		strExec = s.usr.homedir + "/.xinitrc " + strExec
	} else if allowStartupPrefix && s.conf.DbusLaunch && !strings.Contains(strExec, "dbus-launch") {
		s.dbus = &dbus{}
	}

	if startScript {
		cmd = cmdAsUser(s.usr, s.getLoginShell(), strings.Split(strExec, " ")...)
	} else {
		cmd = cmdAsUser(s.usr, strExec)
	}

	return cmd, strExec
}

// Gets preferred login shell
func (s *commonSession) getLoginShell() string {
	if s.d.loginShell != "" {
		return s.d.loginShell
	}
	return "/bin/sh"
}

// Catch interrupt signal chan and interrupts Cmd.
func handleInterrupt(c chan os.Signal, cmd *exec.Cmd) {
	<-c
	interrupted = true
	logPrint("Caught interrupt signal")
	cmd.Process.Signal(os.Interrupt)
	cmd.Wait()
}
