import { REGEXP_ONLY_DIGITS } from 'input-otp'

import { Label } from '@/components/ui/label'
import { InputOTP, InputOTPGroup, InputOTPSeparator, InputOTPSlot } from '@/components/ui/input-otp'

type OtpCodeFieldProps = {
  id: string
  /** Visible field label. */
  label: string
  value: string
  onChange: (value: string) => void
  /** Fired once all six digits are entered — wire this to auto-submit. */
  onComplete?: (value: string) => void
  disabled?: boolean
  autoFocus?: boolean
  /** Render the slots in their error state. */
  invalid?: boolean
  /** id of the element describing the error, for aria-describedby. */
  describedBy?: string
}

// OtpCodeField is the shared six-digit authenticator-code entry used across the
// login, enable, and step-up flows. It wraps the InputOTP primitive with a
// label and the project's error/aria wiring so the three call sites stay in
// lockstep. Numbers only; recovery codes use a plain Input instead.
export function OtpCodeField({
  id,
  label,
  value,
  onChange,
  onComplete,
  disabled,
  autoFocus,
  invalid,
  describedBy,
}: OtpCodeFieldProps) {
  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{label}</Label>
      <InputOTP
        id={id}
        maxLength={6}
        inputMode="numeric"
        pattern={REGEXP_ONLY_DIGITS}
        autoComplete="one-time-code"
        autoFocus={autoFocus}
        disabled={disabled}
        value={value}
        onChange={onChange}
        onComplete={onComplete}
        containerClassName="justify-center"
      >
        <InputOTPGroup>
          {[0, 1, 2].map((i) => (
            <InputOTPSlot key={i} index={i} aria-invalid={invalid || undefined} />
          ))}
        </InputOTPGroup>
        <InputOTPSeparator />
        <InputOTPGroup>
          {[3, 4, 5].map((i) => (
            <InputOTPSlot
              key={i}
              index={i}
              aria-invalid={invalid || undefined}
              aria-describedby={i === 5 && describedBy ? describedBy : undefined}
            />
          ))}
        </InputOTPGroup>
      </InputOTP>
    </div>
  )
}
