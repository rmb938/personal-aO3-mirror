package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/rmb938/personal-aO3-mirror/internal/apiclient"
)

// accountUsages are the account command groups' usage texts, by group.
var accountUsages = map[string]string{
	"invite": `usage: ao3mirror invite <command> [flags]

  invite create [--ttl 24h]    a signup link (default auth.invite_ttl, at most 168h)
  invite list                  every invite and reset link, used or not
  invite revoke ID             stop an unused invite or reset link working

A link is printed once, when it is made: send it to the person it is for.
` + adminSocketNote,
	"user": `usage: ao3mirror user <command> [flags]

  user list                    accounts
  user disable ID              block logins and revoke the account's sessions
  user enable ID               allow logins again and clear failed attempts
  user delete ID               delete the account with its sessions and reset links
  user reset ID --scope password|totp|both [--ttl 24h]
                               a link that replaces the password, the TOTP secret or both
  user sessions ID             the account's sessions

A link is printed once, when it is made: send it to the person it is for.
` + adminSocketNote,
	"session": `usage: ao3mirror session <command>

  session revoke ID            end one session (its refresh token stops working)
` + adminSocketNote,
	"auth": `usage: ao3mirror auth <command>

  auth rotate-key              start a new access-token signing key; tokens signed by the
                               old one keep working until they expire
  auth rotate-master-key       start a new master key and re-encrypt users.db under it
                               (serve also does this every auth.master_key_rotation)
` + adminSocketNote,
}

const adminSocketNote = `
These commands talk to the API's admin socket, so 'ao3mirror serve' must be running.
`

// isAccountCommand reports whether cmd is one of the account command groups in accountUsages.
func isAccountCommand(cmd string) bool {
	_, ok := accountUsages[cmd]
	return ok
}

// usageErr prints group's usage and returns the usage exit code.
func usageErr(errOut io.Writer, group string) int {
	fmt.Fprint(errOut, accountUsages[group])
	return 2
}

// runAccount runs one account command (group is invite, user, session or auth).
func runAccount(ctx context.Context, a *app, group string, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return usageErr(errOut, group)
	}
	cmd, rest := args[0], args[1:]
	switch group + " " + cmd {
	case "invite create":
		return inviteCreate(ctx, a, rest, out, errOut)
	case "invite list":
		return noArgs(errOut, group, rest, func() int { return inviteList(ctx, a, out, errOut) })
	case "user list":
		return noArgs(errOut, group, rest, func() int { return userList(ctx, a, out, errOut) })
	case "user reset":
		return userReset(ctx, a, rest, out, errOut)
	case "user sessions":
		return withID(errOut, group, rest, func(id apiclient.ID) int { return userSessions(ctx, a, id, out, errOut) })
	case "invite revoke", "user disable", "user enable", "user delete", "session revoke":
		return withID(errOut, group, rest, func(id apiclient.ID) int { return accountAction(ctx, a, group, cmd, id, out, errOut) })
	case "auth rotate-key":
		return noArgs(errOut, group, rest, func() int {
			if err := a.client.RotateSigningKey(ctx); err != nil {
				return fail(errOut, "auth rotate-key", err)
			}
			fmt.Fprintln(out, "signing key rotated; tokens signed by the old key work until they expire")
			return 0
		})
	case "auth rotate-master-key":
		return noArgs(errOut, group, rest, func() int {
			if err := a.client.RotateMasterKey(ctx); err != nil {
				return fail(errOut, "auth rotate-master-key", err)
			}
			fmt.Fprintln(out, "master key rotated; users.db is re-encrypted under the new key, and the old key is deleted in 7 days")
			return 0
		})
	}
	return usageErr(errOut, group)
}

// noArgs runs fn when args is empty, otherwise prints group's usage.
func noArgs(errOut io.Writer, group string, args []string, fn func() int) int {
	if len(args) != 0 {
		return usageErr(errOut, group)
	}
	return fn()
}

// withID runs fn with the one positional id in args, otherwise prints group's usage.
func withID(errOut io.Writer, group string, args []string, fn func(apiclient.ID) int) int {
	if len(args) != 1 {
		return usageErr(errOut, group)
	}
	id, ok := parseID(errOut, args[0])
	if !ok {
		return 2
	}
	return fn(id)
}

func inviteCreate(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("invite create", flag.ContinueOnError)
	fs.SetOutput(errOut)
	ttl := fs.Duration("ttl", 0, "how long the link works (default auth.invite_ttl)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr(errOut, "invite")
	}
	req := &apiclient.CreateInviteRequest{}
	if flagSet(fs, "ttl") {
		req.TTL.SetTo(apiclient.Duration(*ttl))
	}
	l, err := a.client.CreateInvite(ctx, req)
	if err != nil {
		return fail(errOut, "invite create", err)
	}
	printLink(out, "signup invite", l)
	return 0
}

func userReset(ctx context.Context, a *app, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("user reset", flag.ContinueOnError)
	fs.SetOutput(errOut)
	scope := fs.String("scope", "", "what the link replaces: password, totp or both")
	ttl := fs.Duration("ttl", 0, "how long the link works (default auth.invite_ttl)")
	arg, ok := parseWithPositional(fs, args)
	if !ok {
		return usageErr(errOut, "user")
	}
	id, ok := parseID(errOut, arg)
	if !ok {
		return 2
	}
	// The client refuses an unknown enum value before sending, with a less helpful message.
	scopes := apiclient.ResetScope("").AllValues()
	if !slices.Contains(scopes, apiclient.ResetScope(*scope)) {
		names := make([]string, len(scopes))
		for i, s := range scopes {
			names[i] = string(s)
		}
		fmt.Fprintf(errOut, "user reset: give --scope %s\n", strings.Join(names, "|"))
		return 2
	}
	req := &apiclient.CreateResetRequest{Scope: apiclient.ResetScope(*scope)}
	if flagSet(fs, "ttl") {
		req.TTL.SetTo(apiclient.Duration(*ttl))
	}
	l, err := a.client.CreateReset(ctx, req, apiclient.CreateResetParams{ID: id})
	if err != nil {
		return fail(errOut, fmt.Sprintf("user reset %d", id), err)
	}
	printLink(out, fmt.Sprintf("%s reset for user %d", *scope, id), l)
	return 0
}

// printLink prints a new invite or reset link, which the API shows only this once.
func printLink(out io.Writer, what string, l *apiclient.InviteLink) {
	fmt.Fprintf(out, "%s created as invite %d, usable until %s:\n%s\n(the link is shown only now; 'ao3mirror invite revoke %d' stops it working)\n",
		what, l.ID, clockTime(l.ExpiresAt), l.URL, l.ID)
}

func inviteList(ctx context.Context, a *app, out, errOut io.Writer) int {
	l, err := a.client.ListInvites(ctx)
	if err != nil {
		return fail(errOut, "invite list", err)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tKIND\tUSER\tSCOPE\tCREATED\tEXPIRES\tUSED\tUSED BY\tREVOKED")
	for _, i := range l.Items {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", i.ID, i.Kind, optID(i.UserID), dash(string(i.Scope.Or(""))),
			clockTime(i.CreatedAt), clockTime(i.ExpiresAt), when(i.UsedAt), optID(i.UsedBy), when(i.RevokedAt))
	}
	tw.Flush()
	return 0
}

func userList(ctx context.Context, a *app, out, errOut io.Writer) int {
	l, err := a.client.ListUsers(ctx)
	if err != nil {
		return fail(errOut, "user list", err)
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tUSERNAME\tCREATED\tLAST LOGIN\tPASSWORD CHANGED\tDISABLED\tFAILED ATTEMPTS\tRECOVERY CODES")
	for _, u := range l.Items {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%d\t%d\n", u.ID, u.Username, clockTime(u.CreatedAt), when(u.LastLoginAt),
			when(u.PasswordChangedAt), when(u.DisabledAt), u.FailedAttempts, u.RecoveryCodesLeft)
	}
	tw.Flush()
	return 0
}

func userSessions(ctx context.Context, a *app, id apiclient.ID, out, errOut io.Writer) int {
	l, err := a.client.ListUserSessions(ctx, apiclient.ListUserSessionsParams{ID: id})
	if err != nil {
		return fail(errOut, fmt.Sprintf("user sessions %d", id), err)
	}
	if len(l.Items) == 0 {
		fmt.Fprintf(out, "user %d has no sessions\n", id)
		return 0
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCREATED\tLAST USED\tEXPIRES")
	for _, s := range l.Items {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", s.ID, clockTime(s.CreatedAt), clockTime(s.LastUsedAt), clockTime(s.ExpiresAt))
	}
	tw.Flush()
	return 0
}

// accountAction runs one of the account changes that take only an id.
func accountAction(ctx context.Context, a *app, group, cmd string, id apiclient.ID, out, errOut io.Writer) int {
	var err error
	var msg string
	switch group + " " + cmd {
	case "invite revoke":
		err, msg = a.client.RevokeInvite(ctx, apiclient.RevokeInviteParams{ID: id}), "revoked (if it was unused)"
	case "user disable":
		err, msg = a.client.DisableUser(ctx, apiclient.DisableUserParams{ID: id}), "disabled (its sessions are revoked)"
	case "user enable":
		err, msg = a.client.EnableUser(ctx, apiclient.EnableUserParams{ID: id}), "enabled"
	case "user delete":
		err, msg = a.client.DeleteUser(ctx, apiclient.DeleteUserParams{ID: id}), "deleted with its sessions and reset links"
	case "session revoke":
		err, msg = a.client.RevokeSession(ctx, apiclient.RevokeSessionParams{ID: id}), "revoked"
	}
	if err != nil {
		return fail(errOut, fmt.Sprintf("%s %s %d", group, cmd, id), err)
	}
	fmt.Fprintf(out, "%s %d %s\n", group, id, msg)
	return 0
}

// clockTime formats a required timestamp like when.
func clockTime(t apiclient.Timestamp) string {
	return whenTime(time.Time(t))
}
