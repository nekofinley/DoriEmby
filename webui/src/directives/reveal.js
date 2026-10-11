/**
 * v-reveal — fade a block in when it scrolls into view.
 *
 *   <section v-reveal>…</section>            the block itself
 *   <div v-reveal.stagger>…</div>            its children, one after another
 *
 * The hidden state is added here (before insert), so if IntersectionObserver is
 * missing the content simply stays visible instead of disappearing.
 */
const OPTIONS = { rootMargin: '0px 0px -12% 0px', threshold: 0.08 }

export const reveal = {
  beforeMount(el, binding) {
    if (typeof IntersectionObserver === 'undefined') return
    if (window.matchMedia?.('(prefers-reduced-motion: reduce)').matches) return
    el.classList.add(binding.modifiers.stagger ? 'reveal-stagger' : 'reveal')
    if (binding.value) el.style.setProperty('--d', `${binding.value}ms`)
  },
  mounted(el) {
    if (!el.classList.contains('reveal') && !el.classList.contains('reveal-stagger')) return
    const io = new IntersectionObserver((entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        e.target.classList.add('in')
        io.unobserve(e.target)
      }
    }, OPTIONS)
    io.observe(el)
    el._revealObserver = io
  },
  unmounted(el) {
    el._revealObserver?.disconnect()
    delete el._revealObserver
  },
}
