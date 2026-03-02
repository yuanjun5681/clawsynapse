<script lang="ts">
  interface Props {
    state?: 'idle' | 'thinking' | 'streaming' | 'done';
    backendStatus?: 'running' | 'starting' | 'stopped';
  }

  let { state = 'idle', backendStatus = 'stopped' }: Props = $props();
</script>

<svg
  class="avatar {state}"
  viewBox="0 0 40 40"
  xmlns="http://www.w3.org/2000/svg"
>
  <!-- Antenna stem -->
  <line x1="20" y1="8" x2="20" y2="3" stroke="#555" stroke-width="1.5" stroke-linecap="round" />
  <!-- Antenna light (status indicator) -->
  <circle
    cx="20" cy="2"
    r="2"
    class="antenna"
    class:antenna-green={backendStatus === 'running'}
    class:antenna-yellow={backendStatus === 'starting'}
    class:antenna-red={backendStatus === 'stopped'}
  />

  <!-- Head -->
  <rect x="6" y="8" width="28" height="24" rx="6" ry="6" fill="none" stroke="var(--accent)" stroke-width="1.5" />

  <!-- Left eye -->
  <circle cx="15" cy="19" r="2.5" class="eye" fill="var(--accent)" />
  <!-- Right eye -->
  <circle cx="25" cy="19" r="2.5" class="eye" fill="var(--accent)" />

  <!-- Happy eyes (shown in done state) -->
  <path d="M12.5 19 Q15 16 17.5 19" class="happy-eye" fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linecap="round" />
  <path d="M22.5 19 Q25 16 27.5 19" class="happy-eye" fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linecap="round" />

  <!-- Mouth (idle/thinking) -->
  <line x1="16" y1="27" x2="24" y2="27" class="mouth-line" stroke="var(--accent)" stroke-width="1.5" stroke-linecap="round" />

  <!-- Mouth (talking - LED marquee) -->
  <rect x="12" y="26" width="2.5" height="2.5" rx="0.5" class="led led-1" fill="var(--accent)" />
  <rect x="16" y="26" width="2.5" height="2.5" rx="0.5" class="led led-2" fill="var(--accent)" />
  <rect x="20" y="26" width="2.5" height="2.5" rx="0.5" class="led led-3" fill="var(--accent)" />
  <rect x="24" y="26" width="2.5" height="2.5" rx="0.5" class="led led-4" fill="var(--accent)" />

  <!-- Mouth (smile for done) -->
  <path d="M15 26 Q20 31 25 26" class="mouth-smile" fill="none" stroke="var(--accent)" stroke-width="1.5" stroke-linecap="round" />
</svg>

<style>
  .avatar {
    width: 56px;
    height: 56px;
    display: block;
  }

  /* --- Antenna colors --- */
  .antenna-green {
    fill: var(--green);
    filter: drop-shadow(0 0 3px var(--green));
  }

  .antenna-yellow {
    fill: var(--yellow);
    animation: antenna-pulse 1.5s ease-in-out infinite;
  }

  .antenna-red {
    fill: var(--red);
  }

  /* --- Default: hide conditional elements --- */
  .happy-eye {
    opacity: 0;
  }

  .led {
    opacity: 0;
  }

  .mouth-smile {
    opacity: 0;
  }

  /* ==================== IDLE ==================== */
  .idle {
    animation: float 3s ease-in-out infinite;
  }

  .idle .eye {
    animation: blink 4s ease-in-out infinite;
  }

  .idle .mouth-line {
    opacity: 1;
  }

  @keyframes float {
    0%, 100% { transform: translateY(0); }
    50% { transform: translateY(-2px); }
  }

  @keyframes blink {
    0%, 90%, 100% { transform: scaleY(1); }
    95% { transform: scaleY(0.1); }
  }

  /* ==================== THINKING ==================== */
  .thinking {
    animation: bounce 1.2s ease-in-out infinite;
  }

  .thinking .eye {
    animation: look-around 1.5s ease-in-out infinite;
  }

  .thinking .antenna {
    animation: antenna-pulse 0.8s ease-in-out infinite;
  }

  .thinking .mouth-line {
    opacity: 1;
  }

  @keyframes bounce {
    0%, 100% { transform: translateY(0); }
    50% { transform: translateY(-1.5px); }
  }

  @keyframes look-around {
    0%, 100% { transform: translateX(0); }
    25% { transform: translateX(-1.5px); }
    75% { transform: translateX(1.5px); }
  }

  @keyframes antenna-pulse {
    0%, 100% { opacity: 1; }
    50% { opacity: 0.3; }
  }

  /* ==================== STREAMING ==================== */
  .streaming .eye {
    opacity: 0.7;
  }

  .streaming .mouth-line {
    opacity: 0;
  }

  .streaming .led {
    animation: marquee 1.2s ease-in-out infinite;
  }

  .streaming .led-1 { animation-delay: 0s; }
  .streaming .led-2 { animation-delay: 0.2s; }
  .streaming .led-3 { animation-delay: 0.4s; }
  .streaming .led-4 { animation-delay: 0.6s; }

  .streaming .antenna {
    fill: var(--accent);
    animation: antenna-glow 1.2s ease-in-out infinite;
  }

  @keyframes marquee {
    0%, 100% { opacity: 0.1; }
    30%, 60% { opacity: 1; filter: drop-shadow(0 0 3px var(--accent)); }
  }

  @keyframes antenna-glow {
    0%, 100% { filter: drop-shadow(0 0 2px var(--accent)); }
    50% { filter: drop-shadow(0 0 6px var(--accent)); }
  }

  /* ==================== DONE ==================== */
  .done {
    animation: done-bounce 0.4s ease-out;
  }

  .done .eye {
    opacity: 0;
  }

  .done .happy-eye {
    opacity: 1;
  }

  .done .mouth-line {
    opacity: 0;
  }

  .done .mouth-smile {
    opacity: 1;
  }

  @keyframes done-bounce {
    0% { transform: scale(1); }
    40% { transform: scale(1.1); }
    100% { transform: scale(1); }
  }
</style>
