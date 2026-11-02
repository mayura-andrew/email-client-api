/** RFC 4180 parser: quoted fields, escaped quotes, CRLF, and an Excel BOM. */
export function parseCSV(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let field = "";
  let quoted = false;
  const input = text.replace(/^﻿/, "");
  for (let i = 0; i < input.length; i++) {
    const char = input[i];
    if (quoted) {
      if (char === '"' && input[i + 1] === '"') { field += '"'; i++; }
      else if (char === '"') quoted = false;
      else field += char;
    } else if (char === '"') quoted = true;
    else if (char === ",") { row.push(field); field = ""; }
    else if (char === "\n" || char === "\r") {
      if (char === "\r" && input[i + 1] === "\n") i++;
      row.push(field); field = "";
      if (row.some(value => value.trim() !== "")) rows.push(row);
      row = [];
    } else field += char;
  }
  row.push(field);
  if (row.some(value => value.trim() !== "")) rows.push(row);
  return rows;
}

// ---------------------------------------------------------------------------
// Email validation. Mirrors internal/validator/email.go exactly so the preview
// accepts precisely what the API will import.
// ---------------------------------------------------------------------------

const LOCAL_CHARS = /^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+$/;
const LABEL_CHARS = /^[a-z0-9-]+$/i;

export function emailProblem(address: string): string | null {
  if (address === "") return "Email is empty";
  if (/[ \t]/.test(address)) return "Email contains spaces";
  const ats = address.split("@").length - 1;
  if (ats === 0) return "Email is missing @";
  if (ats > 1) return "Email has more than one @";
  if (address.length > 254) return "Email is too long";
  const [local, domain] = address.split("@");
  if (!local) return "Nothing before the @";
  if (local.length > 64) return "Part before the @ is too long";
  if (local.startsWith(".") || local.endsWith(".") || local.includes("..")) return "Misplaced dot before the @";
  if (!LOCAL_CHARS.test(local)) return "Invalid character before the @";
  if (!domain) return "Nothing after the @";
  if (!domain.includes(".")) return "Domain is missing a dot (e.g. gmail.com)";
  const labels = domain.split(".");
  for (const label of labels) {
    if (!label) return "Misplaced dot in the domain";
    if (label.length > 63 || label.startsWith("-") || label.endsWith("-") || !LABEL_CHARS.test(label)) return "Invalid domain";
  }
  const tld = labels[labels.length - 1];
  if (tld.length < 2 || !/^[a-z]+$/i.test(tld)) return "Domain ending is not valid";
  return null;
}

// Valid-looking addresses at a misspelled popular domain bounce silently, so
// they are flagged with a one-click suggestion rather than rejected.
const POPULAR = ["gmail.com", "yahoo.com", "hotmail.com", "outlook.com", "icloud.com", "live.com", "googlemail.com", "proton.me"];
const TLD_FIXES: Record<string, string> = { con: "com", cmo: "com", ocm: "com", comm: "com", coom: "com", cm: "com", om: "com", nett: "net", ogr: "org", orgg: "org" };

function distance(a: string, b: string): number {
  const dp = Array.from({ length: a.length + 1 }, (_, i) => [i, ...Array(b.length).fill(0)]);
  for (let j = 1; j <= b.length; j++) dp[0][j] = j;
  for (let i = 1; i <= a.length; i++) {
    for (let j = 1; j <= b.length; j++) {
      dp[i][j] = Math.min(dp[i - 1][j] + 1, dp[i][j - 1] + 1, dp[i - 1][j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1));
      if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) dp[i][j] = Math.min(dp[i][j], dp[i - 2][j - 2] + 1);
    }
  }
  return dp[a.length][b.length];
}

export function suggestEmail(address: string): string | null {
  const at = address.lastIndexOf("@");
  if (at < 0) return null;
  const local = address.slice(0, at);
  const domain = address.slice(at + 1);
  if (POPULAR.includes(domain)) return null;
  const close = POPULAR.find(candidate => distance(domain, candidate) <= 2 && domain.split(".").length === candidate.split(".").length);
  if (close) return `${local}@${close}`;
  const parts = domain.split(".");
  const fix = TLD_FIXES[parts[parts.length - 1]];
  if (fix) return `${local}@${[...parts.slice(0, -1), fix].join(".")}`;
  return null;
}

// ---------------------------------------------------------------------------
// Column mapping
// ---------------------------------------------------------------------------

const NAME_ALIASES = ["student_name", "student name", "name", "full name", "full_name", "fullname", "participant", "participant name", "student"];
const EMAIL_ALIASES = ["email", "e-mail", "email address", "email_address", "mail", "student email", "student_email", "e-mail address"];
const NUMBER_ALIASES = ["certificate_number", "certificate number", "certificate no", "certificate_no", "certificate id", "certificate_id", "cert no", "cert_no", "cert number", "certificate"];

// Matches campaign_recipients.certificate_number.
export const MAX_NUMBER_LENGTH = 128;

/** Column index per field; -1 means no column. The certificate number is optional. */
export type Mapping = { name: number; email: number; number: number };

export function detectMapping(headers: string[]): Mapping {
  const normalized = headers.map(h => h.trim().toLowerCase());
  const find = (aliases: string[]) => {
    for (const alias of aliases) {
      const index = normalized.indexOf(alias);
      if (index >= 0) return index;
    }
    return normalized.findIndex(h => aliases.some(alias => h.includes(alias)));
  };
  return { name: find(NAME_ALIASES), email: find(EMAIL_ALIASES), number: find(NUMBER_ALIASES) };
}

// ---------------------------------------------------------------------------
// Audience analysis
// ---------------------------------------------------------------------------

export type RowStatus = "valid" | "warning" | "invalid" | "duplicate";

export type ManualEntry = { id: number; name: string; email: string; number?: string };

export type AudienceRow = {
  line: number; // line number in the file (header = 1); manual entries continue after the file
  manualId?: number; // set for students added by hand
  cells: string[];
  name: string;
  email: string;
  number: string;
  status: RowStatus;
  message?: string;
  suggestion?: string;
  duplicateOf?: number;
};

export type DuplicateGroup = { email: string; lines: number[]; names: string[]; conflictingNames: boolean };

export type Audience = {
  headers: string[];
  mapping: Mapping;
  rows: AudienceRow[];
  valid: AudienceRow[]; // rows that will be imported (valid + warning)
  duplicates: DuplicateGroup[];
  counts: Record<RowStatus, number>;
  error?: string;
};

/**
 * Validates file rows and hand-added students together, so duplicates are
 * caught across both and the preview shows one consistent list.
 */
export function analyzeAudience(table: string[][], mapping: Mapping, overrides: Record<number, string> = {}, manual: ManualEntry[] = []): Audience {
  const headers = table[0] ?? [];
  const empty: Audience = { headers, mapping, rows: [], valid: [], duplicates: [], counts: { valid: 0, warning: 0, invalid: 0, duplicate: 0 } };
  if (table.length === 0) return { ...empty, error: "The file is empty." };
  if (mapping.name < 0 || mapping.email < 0) return { ...empty, error: "Choose which columns hold the student name and the email address." };
  if (mapping.name === mapping.email) return { ...empty, error: "Name and email must be different columns." };
  if (mapping.number >= 0 && (mapping.number === mapping.name || mapping.number === mapping.email)) return { ...empty, error: "The certificate number must be its own column." };

  const firstSeen = new Map<string, AudienceRow>();
  const groups = new Map<string, DuplicateGroup>();
  const width = Math.max(table[0]?.length ?? 0, mapping.name + 1, mapping.email + 1, mapping.number + 1);
  const manualRows = manual.map(entry => {
    const cells = Array<string>(width).fill("");
    cells[mapping.name] = entry.name;
    cells[mapping.email] = entry.email;
    if (mapping.number >= 0) cells[mapping.number] = entry.number ?? "";
    // Kept even when the file has no number column, so a typed number is not lost.
    return { cells, manualId: entry.id as number | undefined, number: entry.number ?? "" };
  });
  const sources = [...table.slice(1).map(cells => ({ cells, manualId: undefined as number | undefined, number: cells[mapping.number] ?? "" })), ...manualRows];
  const rows: AudienceRow[] = sources.map(({ cells, manualId, number: rawNumber }, index) => {
    const line = index + 2;
    const name = (cells[mapping.name] ?? "").trim();
    const email = (overrides[line] ?? cells[mapping.email] ?? "").trim().toLowerCase();
    const number = rawNumber.trim();
    const row: AudienceRow = { line, cells, name, email, number, status: "valid", manualId };
    const problem = emailProblem(email);
    if (cells.length <= Math.max(mapping.name, mapping.email)) { row.status = "invalid"; row.message = "Row is missing columns"; }
    else if (!name) { row.status = "invalid"; row.message = "Student name is empty"; }
    else if (number.length > MAX_NUMBER_LENGTH) { row.status = "invalid"; row.message = "Certificate number is too long"; }
    else if (problem) { row.status = "invalid"; row.message = problem; }
    else if (firstSeen.has(email)) {
      const first = firstSeen.get(email)!;
      row.status = "duplicate";
      row.duplicateOf = first.line;
      row.message = `Same email as row ${first.line}`;
      const group = groups.get(email) ?? { email, lines: [first.line], names: [first.name], conflictingNames: false };
      group.lines.push(line);
      group.names.push(name);
      group.conflictingNames = new Set(group.names.map(n => n.toLowerCase())).size > 1;
      groups.set(email, group);
    } else {
      firstSeen.set(email, row);
      const suggestion = suggestEmail(email);
      if (suggestion) { row.status = "warning"; row.suggestion = suggestion; row.message = `Possible typo in ${email.split("@")[1]}`; }
    }
    return row;
  });

  const counts = { valid: 0, warning: 0, invalid: 0, duplicate: 0 };
  for (const row of rows) counts[row.status]++;
  return {
    headers, mapping, rows,
    valid: rows.filter(row => row.status === "valid" || row.status === "warning"),
    duplicates: [...groups.values()],
    counts,
  };
}

/** CSV with exactly the columns the API expects, built from the mapped and corrected rows. */
export function toUploadCSV(rows: AudienceRow[]): string {
  const quote = (value: string) => /[",\r\n]/.test(value) ? `"${value.replaceAll('"', '""')}"` : value;
  return ["student_name,email,certificate_number", ...rows.map(row => `${quote(row.name)},${quote(row.email)},${quote(row.number)}`)].join("\n");
}

/**
 * Reads a pasted list, one person per line, in the forms people usually copy:
 * "Name <email>", "Name, email", "email, Name", tab-separated, or just "email".
 */
export function parsePastedList(text: string): { name: string; email: string }[] {
  return text.split(/\r?\n/).map(line => line.trim()).filter(Boolean).map(line => {
    const angle = line.match(/^(.*?)<([^>]+)>\s*$/);
    if (angle) return { name: angle[1].replace(/["',;]+/g, " ").trim(), email: angle[2].trim().toLowerCase() };
    const parts = line.split(/[,;\t]/).map(part => part.trim().replace(/^"|"$/g, "")).filter(Boolean);
    const emailIndex = parts.findIndex(part => part.includes("@"));
    if (emailIndex < 0) return { name: parts.join(" "), email: "" };
    const name = parts.filter((_, i) => i !== emailIndex).join(" ");
    return { name, email: parts[emailIndex].toLowerCase() };
  });
}
