package bufauth

import "syscall"

// SocketPair returns a connected, close-on-exec SOCK_STREAM Unix domain
// socket pair for tests and in-process proxy bridges.
func SocketPair() ([2]int, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return [2]int{}, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	return fds, nil
}

// SendFD writes token alongside one file descriptor using SCM_RIGHTS. The
// caller is expected to have already registered the transfer with
// BeginTransfer; the descriptor passed here is normally Transfer.FD.
func SendFD(sockFD int, token string, fd int) error {
	rights := syscall.UnixRights(fd)
	_, err := syscall.SendmsgN(sockFD, []byte(token), rights, nil, 0)
	return err
}

// ReceiveFD reads one token and exactly one ancillary file descriptor from
// a socket. The returned fd is owned by the caller. The token must still be
// presented to Authority.Claim before the buffer may be used.
func ReceiveFD(sockFD int) (token string, fd int, err error) {
	buf := make([]byte, 32)
	oob := make([]byte, syscall.CmsgSpace(4))
	_, _, _, _, recvErr := syscall.Recvmsg(sockFD, buf, oob, 0)
	if recvErr != nil {
		return "", -1, recvErr
	}
	messages, parseErr := syscall.ParseSocketControlMessage(oob)
	if parseErr != nil {
		return "", -1, parseErr
	}
	if len(messages) != 1 {
		return "", -1, ErrTransferGone
	}
	descs, parseErr := syscall.ParseUnixRights(&messages[0])
	if parseErr != nil {
		return "", -1, parseErr
	}
	if len(descs) != 1 {
		return "", -1, ErrTransferGone
	}
	// Trim trailing zero bytes inserted when the token is shorter than buf.
	end := len(buf)
	for end > 0 && buf[end-1] == 0 {
		end--
	}
	return string(buf[:end]), descs[0], nil
}
