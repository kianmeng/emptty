package src

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"sort"
	"strings"
)

const (
	strCleanScreen = "\x1b[H\x1b[2J"
	pathIssue      = "/etc/issue"
)

// IssueVariable defines list of all escape sequences found in issue file
type issueVariable struct {
	issue string
	char  byte
	arg   string
}

// Starts emptty as daemon spawning emptty on defined TTY.
func startDaemon(conf *config) *os.File {
	fTTY, err := os.OpenFile(conf.ttyPath(), os.O_RDWR, 0700)
	if err != nil {
		logFatal(err)
	}

	if conf.EnableNumlock {
		setKeyboardLeds(fTTY, false, true, false)
	}

	clearScreen(fTTY)

	os.Stdout = fTTY
	os.Stderr = fTTY
	os.Stdin = fTTY

	setColors(conf.FgColor, conf.BgColor)
	clearScreen(fTTY)

	if conf.PrintIssue {
		fmt.Println()
		printIssue(pathIssue, conf.strTTY())
		setColors(conf.FgColor, conf.BgColor)
	}

	switchTTY(conf)

	return fTTY
}

// Stops daemon mode and closes opened TTY.
func stopDaemon(conf *config, fTTY *os.File) {
	resetColors()
	clearScreen(fTTY)

	if fTTY != nil {
		fTTY.Close()
	}
}

// Clears terminal screen
func clearScreen(w io.Writer) {
	if w == nil {
		fmt.Print(strCleanScreen)
	} else {
		w.Write([]byte(strCleanScreen))
	}
}

// Perform switch to defined TTY, if switchTTY is true and tty is greater than 0.
func switchTTY(conf *config) bool {
	if conf.SwitchTTY && conf.Tty > 0 {
		runSimpleCmd("chvt", conf.strTTY())
		return true
	}
	return false
}

// Prints getty issue
func printIssue(path, strTTY string) {
	if fileExists(path) {
		bIssue, err := ioutil.ReadFile(path)
		if err == nil {
			issue := string(bIssue)
			issue = evaluateIssueVars(issue, findUniqueIssueVars(issue), strTTY)

			for issue[len(issue)-2:] == "\n\n" {
				issue = issue[:len(issue)-1]
			}

			fmt.Print(revertColorEscaping(issue))
		}
	}
}

// Finds all unique issue escape sequences
func findUniqueIssueVars(issue string) []*issueVariable {
	var result []*issueVariable
	var knownIssues []string

	saveData := false
	var buffer strings.Builder
	var varName byte
	var arg strings.Builder

	for i := 0; i < len(issue); i++ {
		b := issue[i]

		if b == '\\' {
			saveData = true
			buffer.Reset()
			arg.Reset()
		}

		if saveData {
			if i > 0 {
				if issue[i-1] == '\\' {
					varName = b
				} else if b != '{' && b != '}' && b != '\\' {
					arg.WriteByte(b)
				}
			}
			buffer.WriteByte(b)
			if i == (len(issue)-1) || (i < len(issue) && i > 0 && issue[i-1] == '\\' && issue[i+1] != '{') || b == '}' {
				if !contains(knownIssues, buffer.String()) {
					result = append(result, &issueVariable{buffer.String(), varName, arg.String()})
					knownIssues = append(knownIssues, buffer.String())
				}

				saveData = false
			}
		}
	}

	return result
}

// Evaluates outputs for all known escape sequences and return replaced issue
func evaluateIssueVars(issue string, issueVars []*issueVariable, strTTY string) string {
	result := issue

	sort.Slice(issueVars, func(i int, j int) bool {
		return len(issueVars[i].arg) > len(issueVars[j].arg)
	})

	for _, issueVar := range issueVars {
		output := ""
		processed := true

		switch issueVar.char {
		case 'd':
			output = runSimpleCmd("date")
		case 'l':
			output = getCurrentTTYName(strTTY, false)
		case 'm':
			output = runSimpleCmd("uname", "-m")
		case 'n':
			output = runSimpleCmd("uname", "-n")
		case 'O':
			output = getDnsDomainName()
		case 'r':
			output = runSimpleCmd("uname", "-r")
		case 's':
			output = runSimpleCmd("uname", "-s")
		case 'S':
			output = getOsReleaseValue(issueVar.arg)
		case 't':
			output = runSimpleCmd("date", "+%T")
		case '4', '6':
			output = getIpAddress(issueVar.arg, issueVar.char)
		default:
			processed = false
		}

		if processed {
			result = strings.ReplaceAll(result, issueVar.issue, output)
		}
	}

	return result
}
