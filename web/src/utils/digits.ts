// Persian and Arabic-Indic digits are what a Persian keyboard types by default.
// They are digits like any other, but neither Number() nor a /[0-9]/ filter
// knows that: the first gives NaN, the second drops what was typed.
export const toLatinDigits = (input: string): string =>
  input
    .replace(/[۰-۹]/g, (d) => String(d.charCodeAt(0) - 0x06f0))
    .replace(/[٠-٩]/g, (d) => String(d.charCodeAt(0) - 0x0660));
