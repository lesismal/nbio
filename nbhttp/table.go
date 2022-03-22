// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package nbhttp

import "strings"

var (
	validMethods = map[string]bool{
		"OPTIONS": true,
		"GET":     true,
		"HEAD":    true,
		"POST":    true,
		"PUT":     true,
		"DELETE":  true,
		"TRACE":   true,
		"CONNECT": true,
		"PATCH":   true, // RFC 5789

		// http 2.0
		"PRI": true,
	}

	tokenCharMap = [256]bool{
		'!':  true,
		'#':  true,
		'$':  true,
		'%':  true,
		'&':  true,
		'\'': true,
		'*':  true,
		'+':  true,
		'-':  true,
		'.':  true,
		'0':  true,
		'1':  true,
		'2':  true,
		'3':  true,
		'4':  true,
		'5':  true,
		'6':  true,
		'7':  true,
		'8':  true,
		'9':  true,
		'A':  true,
		'B':  true,
		'C':  true,
		'D':  true,
		'E':  true,
		'F':  true,
		'G':  true,
		'H':  true,
		'I':  true,
		'J':  true,
		'K':  true,
		'L':  true,
		'M':  true,
		'N':  true,
		'O':  true,
		'P':  true,
		'Q':  true,
		'R':  true,
		'S':  true,
		'T':  true,
		'U':  true,
		'W':  true,
		'V':  true,
		'X':  true,
		'Y':  true,
		'Z':  true,
		'^':  true,
		'_':  true,
		'`':  true,
		'a':  true,
		'b':  true,
		'c':  true,
		'd':  true,
		'e':  true,
		'f':  true,
		'g':  true,
		'h':  true,
		'i':  true,
		'j':  true,
		'k':  true,
		'l':  true,
		'm':  true,
		'n':  true,
		'o':  true,
		'p':  true,
		'q':  true,
		'r':  true,
		's':  true,
		't':  true,
		'u':  true,
		'v':  true,
		'w':  true,
		'x':  true,
		'y':  true,
		'z':  true,
		'|':  true,
		'~':  true,
	}

	// headerCharMap = [256]bool{}.

	numCharMap      = [256]bool{}
	hexCharMap      = [256]bool{}
	alphaCharMap    = [256]bool{}
	alphaNumCharMap = [256]bool{}

	validMethodCharMap = [256]bool{}
)

func init() {
	var dis byte = 'a' - 'A'

	for m := range validMethods {
		for _, c := range m {
			validMethodCharMap[c] = true
			validMethodCharMap[byte(c)+dis] = true
		}
	}

	for i := byte(0); i < 10; i++ {
		numCharMap['0'+i] = true
		alphaNumCharMap['0'+i] = true
		hexCharMap['0'+i] = true
	}
	for i := byte(0); i < 6; i++ {
		hexCharMap['A'+i] = true
		hexCharMap['a'+i] = true
	}

	for i := byte(0); i < 26; i++ {
		alphaCharMap['A'+i] = true
		alphaCharMap['A'+i+dis] = true
		alphaNumCharMap['A'+i] = true
		alphaNumCharMap['A'+i+dis] = true
	}

	// for i := 0; i < len(tokenCharMap); i++ {
	// 	headerCharMap[i] = tokenCharMap[i]
	// }
	// headerCharMap[':'] = true
	// headerCharMap['?'] = true
}

func isAlpha(c byte) bool {
	return alphaCharMap[c]
}

func isNum(c byte) bool {
	return numCharMap[c]
}

func isHex(c byte) bool {
	return hexCharMap[c]
}

// func isAlphaNum(c byte) bool {
// 	return alphaNumCharMap[c]
// }

func isToken(c byte) bool {
	return tokenCharMap[c]
}

func isValidMethod(m string) bool {
	return validMethods[strings.ToUpper(m)]
}

func isValidMethodChar(c byte) bool {
	return validMethodCharMap[c]
}
