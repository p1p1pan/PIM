// 统一顺序加载脚本，避免在 HTML 中堆叠大量 script 标签。
// 如果某个脚本加载失败，会带上具体 src，便于快速定位白屏问题。
window.loadScriptsInOrder = async function loadScriptsInOrder(sources) {
  for (const src of sources) {
    // eslint-disable-next-line no-await-in-loop
    await new Promise((resolve, reject) => {
      const s = document.createElement("script");
      s.src = src;
      s.async = false;
      s.onload = resolve;
      s.onerror = () => reject(new Error(`加载脚本失败: ${src}`));
      document.body.appendChild(s);
    });
  }
};
