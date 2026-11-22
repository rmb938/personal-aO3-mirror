# Common-password list

`common-passwords.txt.gz` is the blocklist `CheckPassword` uses (NIST SP 800-63B-4 §3.1.1.2: a
password SHALL be checked against a list of commonly used or compromised values).

- Source: SecLists, `Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt`
  (the UK NCSC's top 100,000 passwords from Have I Been Pwned), commit
  `64e7d0b910015fa4e8dffa9fed7aba7d73e4673d` of https://github.com/danielmiessler/SecLists.
  Downloaded file SHA-256: `c2e5696882c603b76bb67a47ee970897e5a76fc4c3f5547abe3d0ca340c576e0`
  (99,840 lines).
- Licence: MIT, Copyright (c) 2018 Daniel Miessler; the full text is in
  `common-passwords.LICENSE`.
- Processing (`gen_common_passwords.go`): NFKC, lower case, keep 8 to 128 runes (shorter or longer
  entries fail the length rule anyway), de-duplicate, sort, gzip. Result: 46,483 entries.

To rebuild (from this directory):

```sh
curl -sSfL -o /tmp/ncsc.txt \
  https://raw.githubusercontent.com/danielmiessler/SecLists/64e7d0b910015fa4e8dffa9fed7aba7d73e4673d/Passwords/Common-Credentials/100k-most-used-passwords-NCSC.txt
sha256sum /tmp/ncsc.txt   # must match the value above
go run gen_common_passwords.go -in /tmp/ncsc.txt -out common-passwords.txt.gz
```
