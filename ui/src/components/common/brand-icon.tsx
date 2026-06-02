import { type IconType } from 'react-icons'
import { SiGrafana, SiMariadb, SiPostgresql } from 'react-icons/si'

// Brand glyphs + colors for known add-on templates and managed-DB engines,
// keyed by the template's manifest icon key (e.g. "grafana") or the engine name
// ("postgres" / "mariadb"). Unknown keys fall back to a generic icon at the
// call site.
const BRANDS: Record<string, { Icon: IconType; color: string }> = {
  grafana: { Icon: SiGrafana, color: '#F46800' },
  postgres: { Icon: SiPostgresql, color: '#4169E1' },
  postgresql: { Icon: SiPostgresql, color: '#4169E1' },
  mariadb: { Icon: SiMariadb, color: '#003545' },
}

export function brandFor(key?: string | null) {
  if (!key) return null
  return BRANDS[key.toLowerCase()] ?? null
}

// BrandIcon renders the brand glyph in its brand color, or nothing when the key
// is unknown — render a fallback (letter avatar, generic icon) yourself when it
// returns null.
export function BrandIcon({ brand, className }: { brand?: string | null; className?: string }) {
  const b = brandFor(brand)
  if (!b) return null
  const { Icon, color } = b
  return <Icon className={className} style={{ color }} aria-hidden />
}
