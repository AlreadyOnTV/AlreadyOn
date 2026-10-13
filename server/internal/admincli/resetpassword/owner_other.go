//go:build !unix

package resetpassword

func dropToDataOwner(string) error { return nil }
