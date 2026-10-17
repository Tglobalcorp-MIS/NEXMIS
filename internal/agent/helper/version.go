package helper

// Version is the helper build version. The caller (cmd/nexmis-agent) sets it
// from the agent's version variable before invoking Main; it is reported to the
// service in the Hello message and shown in the "about" dialog.
var Version = "dev"
