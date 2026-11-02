"use client";

import { FormEvent, useRef, useState } from "react";
import { emailProblem, MAX_NUMBER_LENGTH, suggestEmail } from "@/lib/csv";
import { Icon } from "./Icon";
import { Button } from "./ui";

/**
 * One name + email (+ optional certificate number) entry with the same checks
 * the CSV import applies, shown as the user types. `existing` holds emails
 * already in the list (lowercase).
 */
export function RecipientForm({ existing, onAdd, submitLabel = "Add student", autoFocus }: {
  existing: Set<string>;
  onAdd: (name: string, email: string, number: string) => Promise<void> | void;
  submitLabel?: string;
  autoFocus?: boolean;
}) {
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [number, setNumber] = useState("");
  const [touched, setTouched] = useState(false);
  const [busy, setBusy] = useState(false);
  const [serverError, setServerError] = useState("");
  const nameInput = useRef<HTMLInputElement>(null);

  const normalized = email.trim().toLowerCase();
  const nameError = !name.trim() ? "Enter the student's name" : "";
  const emailError = !normalized ? "Enter an email address"
    : emailProblem(normalized) ?? (existing.has(normalized) ? "This email is already in the list" : "");
  const numberError = number.trim().length > MAX_NUMBER_LENGTH ? "Certificate number is too long" : "";
  const suggestion = !emailError ? suggestEmail(normalized) : null;
  const showEmailError = (touched || email.includes("@")) && !!emailError;

  async function submit(event: FormEvent) {
    event.preventDefault();
    setTouched(true);
    if (nameError || emailError || numberError) return;
    setBusy(true); setServerError("");
    try {
      await onAdd(name.trim(), normalized, number.trim());
      setName(""); setEmail(""); setNumber(""); setTouched(false);
      nameInput.current?.focus();
    } catch (err) {
      setServerError((err as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="recipient-form" onSubmit={submit} noValidate>
      <label className={`field ${touched && nameError ? "has-error" : ""}`}>Student name
        <input ref={nameInput} autoFocus={autoFocus} value={name} placeholder="Ada Lovelace" onChange={e => setName(e.target.value)} />
        {touched && nameError && <span className="field-error">{nameError}</span>}
      </label>
      <label className={`field ${showEmailError ? "has-error" : ""}`}>Email
        <input type="email" value={email} placeholder="ada@example.org" onChange={e => { setEmail(e.target.value); setServerError(""); }} />
        {showEmailError && <span className="field-error">{emailError}</span>}
        {suggestion && (
          <span className="field-suggestion">
            <Icon name="alert" size={12} />
            <span>Did you mean <strong>{suggestion}</strong>?</span>
            <button type="button" onClick={() => setEmail(suggestion)}>Use it</button>
          </span>
        )}
      </label>
      <label className={`field ${numberError ? "has-error" : ""}`}><span className="field-label">Certificate number <span className="field-hint">Optional</span></span>
        <input value={number} placeholder="CERT-000001" onChange={e => setNumber(e.target.value)} />
        {numberError && <span className="field-error">{numberError}</span>}
      </label>
      <Button type="submit" icon="plus" loading={busy}>{submitLabel}</Button>
      {serverError && <p className="form-error span-all"><Icon name="alert" size={14} /> {serverError}</p>}
    </form>
  );
}
