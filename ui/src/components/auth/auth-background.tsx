import { useEffect, useRef } from 'react'
import {
  m,
  useMotionTemplate,
  useMotionValue,
  useReducedMotion,
  useSpring,
  useTransform,
} from 'motion/react'

const SPRING = { stiffness: 60, damping: 20, mass: 0.5 } as const

/* Animated, theme-aware backdrop for the auth screens (login / setup).
 *
 * A faint dot lattice + corner grid that light up under a cursor-following
 * "scan" — a radial spotlight that tracks the pointer across the page and,
 * while the pointer is away, wanders on its own so the effect is never static
 * (also covers touch). A soft brand aurora drifts along with it via parallax.
 *
 * All layers are decorative + aria-hidden and sit behind the auth card. Neutral
 * by design: dots and grid lines are foreground-tinted (see index.css), so they
 * read on both light and dark surfaces as the token flips with the theme.
 *
 * Honors prefers-reduced-motion: no listeners, the scan is parked at center,
 * and the m.* mount/loop animations collapse (the global MotionConfig
 * reducedMotion="user" disables transform/loop animations under the OS setting).
 *
 * Mount this as a direct child of a `relative` host; the scan is measured
 * against that parent's bounds.
 */
export function AuthBackground() {
  const reduce = useReducedMotion()
  const ref = useRef<HTMLDivElement>(null)
  const mx = useMotionValue(50)
  const my = useMotionValue(42)
  const sx = useSpring(mx, SPRING)
  const sy = useSpring(my, SPRING)
  const mask = useMotionTemplate`radial-gradient(circle 230px at ${sx}% ${sy}%, #000 0%, rgba(0,0,0,0.85) 34%, transparent 66%)`

  // Parallax: the aurora's mask center rides the scan, but more gently than the
  // spotlight. The dot layer itself stays put (so its dots keep aligning with
  // the base lattice); only the mask drifts.
  const auroraX = useTransform(sx, (v) => (v - 50) * 0.3)
  const auroraY = useTransform(sy, (v) => (v - 42) * 0.3)
  const auroraMask = useMotionTemplate`radial-gradient(circle 300px at calc(50% + ${auroraX}px) calc(110px + ${auroraY}px), #000 0%, rgba(0,0,0,0.55) 42%, transparent 72%)`

  useEffect(() => {
    if (reduce) return
    const host = ref.current?.parentElement
    if (!host) return

    let inside = false
    let raf = 0

    const setPos = (e: PointerEvent) => {
      const r = host.getBoundingClientRect()
      mx.set(((e.clientX - r.left) / r.width) * 100)
      my.set(((e.clientY - r.top) / r.height) * 100)
    }
    const onEnter = (e: PointerEvent) => {
      inside = true
      setPos(e)
    }
    const onMove = (e: PointerEvent) => {
      inside = true
      setPos(e)
    }
    // Hand control back to the idle wander only once the pointer leaves.
    const onLeave = () => {
      inside = false
    }

    const loop = () => {
      if (!inside) {
        const a = performance.now() / 3600
        mx.set(50 + Math.cos(a) * 28)
        my.set(44 + Math.sin(a * 1.3) * 20)
      }
      raf = requestAnimationFrame(loop)
    }

    host.addEventListener('pointerenter', onEnter)
    host.addEventListener('pointermove', onMove)
    host.addEventListener('pointerleave', onLeave)
    raf = requestAnimationFrame(loop)
    return () => {
      host.removeEventListener('pointerenter', onEnter)
      host.removeEventListener('pointermove', onMove)
      host.removeEventListener('pointerleave', onLeave)
      cancelAnimationFrame(raf)
    }
  }, [reduce, mx, my])

  const maskStyle = { maskImage: mask, WebkitMaskImage: mask }

  return (
    <m.div
      ref={ref}
      className="pointer-events-none absolute inset-0 z-0 overflow-hidden"
      aria-hidden
      initial={reduce ? false : { opacity: 0 }}
      animate={reduce ? undefined : { opacity: 1 }}
      transition={{ duration: 1.2, ease: 'easeOut' }}
    >
      {/* Soft brand aurora — a glow made of lit dots: a bright brand-tinted dot
          layer revealed through a soft radial mask that fades at its edges, so
          the glow only ever shows on the dots. Breathes via opacity, drifts via
          the mask center (the layer stays grid-aligned). */}
      <m.div
        className="auth-aurora-dots"
        style={{ maskImage: auroraMask, WebkitMaskImage: auroraMask }}
        animate={reduce ? undefined : { opacity: [0.55, 0.85, 0.55] }}
        transition={{ duration: 10, ease: 'easeInOut', repeat: Infinity }}
      />

      {/* Dot lattice: faint base + bright copy revealed only under the scan. */}
      <div className="scan-field" />
      <m.div className="scan-field-bright" style={maskStyle} />

      {/* Corner grid: faint base + bright copy revealed under the scan. */}
      <div className="auth-grid" />
      <div className="auth-corner-clip">
        <m.div className="auth-grid-bright" style={maskStyle} />
      </div>
    </m.div>
  )
}
