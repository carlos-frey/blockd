// blockd — landing page: detecta o sistema, ajusta o botão principal, mostra a
// versão e o tamanho dos arquivos da última release e copia comandos.
(() => {
  "use strict";

  const REPO = "carlos-frey/blockd";
  const DL = `https://github.com/${REPO}/releases/latest/download/`;
  const ICONS = {
    linux: document.querySelector('[data-os-card="linux"] .os-ico'),
    windows: document.querySelector('[data-os-card="windows"] .os-ico'),
  };
  const TARGETS = {
    linux: { file: "blockd-x86_64.AppImage", label: "Baixar para Linux", sub: "AppImage · x86_64" },
    windows: { file: "blockd-setup.exe", label: "Baixar para Windows", sub: "Instalador · 10 e 11, 64 bits" },
  };

  function detectOS() {
    const ua = navigator.userAgent;
    const plat = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
    if (/android|iphone|ipad|ipod/i.test(ua)) return "mobile";
    if (/win/i.test(plat) || /Windows/.test(ua)) return "windows";
    if (/mac/i.test(plat) || /Macintosh/.test(ua)) return "mac";
    if (/linux|x11|cros/i.test(plat) || /Linux|X11/.test(ua)) return "linux";
    return "other";
  }

  const os = new URLSearchParams(location.search).get("os") || detectOS();
  document.body.dataset.os = os;

  const primary = document.querySelector('[data-dl="primary"]');
  const alt = document.querySelector("[data-alt]");
  const t = TARGETS[os];
  if (t) {
    const other = os === "linux" ? "windows" : "linux";
    primary.href = DL + t.file;
    primary.querySelector("[data-label]").textContent = t.label;
    primary.querySelector("[data-sub]").textContent = t.sub;
    const ico = ICONS[os].cloneNode(true);
    ico.removeAttribute("class");
    primary.querySelector("[data-icon]").append(ico);
    alt.innerHTML = `Usa ${other === "windows" ? "Windows" : "Linux"}? ` +
      `<a href="${DL + TARGETS[other].file}">Baixe a versão para ${other === "windows" ? "Windows" : "Linux"}</a> · ` +
      `<a href="#download">instruções</a>`;
    alt.hidden = false;
  } else if (os === "mac") {
    document.querySelector("[data-mac]").hidden = false;
    alt.textContent = "Por enquanto, só Linux e Windows. O macOS não é suportado.";
    alt.hidden = false;
  }

  // Última release: versão, data e tamanho de cada arquivo. Se falhar, a página
  // continua funcionando com os links fixos de /releases/latest/download/.
  const fmtSize = (b) => `${(b / 1048576).toFixed(b < 10485760 ? 1 : 0).replace(".", ",")} MB`;
  fetch(`https://api.github.com/repos/${REPO}/releases/latest`, { headers: { Accept: "application/vnd.github+json" } })
    .then((r) => {
      if (r.status === 404) throw Object.assign(new Error("sem release"), { none: true });
      if (!r.ok) throw new Error(r.status);
      return r.json();
    })
    .then((rel) => {
      const when = new Date(rel.published_at).toLocaleDateString("pt-BR", { day: "numeric", month: "long", year: "numeric" });
      const v = document.querySelector("[data-version]");
      v.textContent = `Versão ${rel.tag_name.replace(/^v/, "")}, de ${when}.`;
      for (const a of document.querySelectorAll("[data-asset]")) {
        const asset = rel.assets.find((x) => x.name === a.dataset.asset);
        if (asset) a.querySelector("[data-size]").textContent = `· ${fmtSize(asset.size)}`;
      }
    })
    .catch((err) => {
      if (!err.none) return;
      document.querySelector("[data-norelease]").hidden = false;
      for (const a of document.querySelectorAll("[data-asset], [data-dl='primary']")) {
        a.href = `https://github.com/${REPO}#readme`;
      }
    });

  // Copiar comandos.
  for (const b of document.querySelectorAll("[data-copy]")) {
    b.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(b.dataset.copy);
        b.textContent = "Copiado";
        b.classList.add("done");
        setTimeout(() => { b.textContent = "Copiar"; b.classList.remove("done"); }, 1600);
      } catch { /* sem permissão de área de transferência: o texto continua selecionável */ }
    });
  }
})();
