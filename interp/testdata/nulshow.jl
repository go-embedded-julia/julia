# repr of strings (print_quoted, escape_string), one per line as
# `<hex of the bytes> <repr>`; quote.golden is this script's output.
for s in ["", "\0\0\0", "\0", "\x001", "\x008", "\0a", "a\0b", "\x01", "\x7f", "\e", "\x00\x07",
        "\x0018", "\$x", "\"q\"", "\\", "\t\n\r\v\f\a\b", "é", "αβ", "​", "​a", "​g",
        "\u85", string('\u85', '1'), "\U1f600", "\U10ffff", string('\U10ffff', '0'), "\Ue0001",
        string('\Ue0001', 'F'), "\xff", "\xc3",
        "\xc3(", "\xe2\x82", "\xe2\x82(", "\xf0\x9f\x98", "\xc0\x80", "\xe0\x80\x80", "\xf8\x88",
        "\x80", "\xbf\xbf", "a\xffb", "퟿", "", "￿", "�"]
    println(bytes2hex(codeunits(s)), " ", repr(s))
end
