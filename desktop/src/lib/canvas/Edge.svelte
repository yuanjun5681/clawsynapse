<script lang="ts">
  interface Props {
    x1: number;
    y1: number;
    x2: number;
    y2: number;
    status: "online" | "offline" | "pending";
    id: string;
  }

  let { x1, y1, x2, y2, status, id }: Props = $props();

  let isOnline = $derived(status === "online");
  let isPending = $derived(status === "pending");
  let strokeColor = $derived(
    isOnline ? "var(--green)" : isPending ? "var(--yellow)" : "var(--text-muted)"
  );
</script>

<g class="edge">
  <!-- Path for particle motion -->
  <path
    {id}
    d="M {x1} {y1} L {x2} {y2}"
    fill="none"
    stroke={strokeColor}
    stroke-width={isOnline ? 1.5 : 1}
    stroke-dasharray={isOnline ? "none" : "6 4"}
    opacity={isOnline ? 0.5 : 0.25}
    class:glow-line={isOnline}
  />

  <!-- Particle animation (online only) -->
  {#if isOnline}
    <circle r="2.5" fill="var(--green)" opacity="0.8">
      <animateMotion dur="3s" repeatCount="indefinite">
        <mpath href="#{id}" />
      </animateMotion>
    </circle>
    <circle r="2.5" fill="var(--green)" opacity="0.8">
      <animateMotion dur="3s" repeatCount="indefinite" begin="1.5s">
        <mpath href="#{id}" />
      </animateMotion>
    </circle>
  {/if}
</g>

<style>
  .glow-line {
    filter: drop-shadow(0 0 var(--glow-spread) rgba(var(--accent-rgb), calc(var(--glow-opacity) * 0.3)));
  }
</style>
