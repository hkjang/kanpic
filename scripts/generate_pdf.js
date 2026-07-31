const fs = require('fs');
const path = require('path');
const { chromium } = require(path.join(__dirname, '../web/node_modules/playwright-core'));

function mdToHtml(md) {
  let html = md;

  // Mermaid diagrams
  html = html.replace(/```mermaid\n([\s\S]*?)```/g, (match, content) => {
    return `<div class="mermaid-container"><div class="mermaid">${content.trim()}</div></div>`;
  });

  // Code blocks
  html = html.replace(/```(\w*)\n([\s\S]*?)```/g, (match, lang, code) => {
    return `<pre><code class="language-${lang}">${code.replace(/</g, '&lt;').replace(/>/g, '&gt;')}</code></pre>`;
  });

  // Callouts > [!NOTE], > [!TIP], > [!WARNING]
  html = html.replace(/^>\s*\[\!(NOTE|TIP|WARNING|IMPORTANT|CAUTION)\]\s*\n((?:^>.*$\n?)+)/gim, (match, type, body) => {
    const cleanBody = body.replace(/^>\s?/gm, '').trim();
    const typeClass = type.toLowerCase();
    const titles = {
      note: '참고 (Note)',
      tip: '팁 (Tip)',
      warning: '경고 (Warning)',
      important: '중요 (Important)',
      caution: '주의 (Caution)'
    };
    return `<div class="callout callout-${typeClass}">
      <div class="callout-title">${titles[typeClass] || type}</div>
      <div class="callout-content">${cleanBody}</div>
    </div>`;
  });

  // Inline code
  html = html.replace(/`([^`]+)`/g, '<code>$1</code>');

  // Tables
  const lines = html.split('\n');
  let inTable = false;
  let tableBuffer = [];
  let newLines = [];

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line.startsWith('|') && line.endsWith('|')) {
      if (!inTable) {
        inTable = true;
        tableBuffer = [line];
      } else {
        tableBuffer.push(line);
      }
    } else {
      if (inTable) {
        newLines.push(renderTable(tableBuffer));
        inTable = false;
        tableBuffer = [];
      }
      newLines.push(lines[i]);
    }
  }
  if (inTable) {
    newLines.push(renderTable(tableBuffer));
  }
  html = newLines.join('\n');

  // Headings
  html = html.replace(/^### (.*$)/gim, '<h3>$1</h3>');
  html = html.replace(/^## (.*$)/gim, '<h2>$1</h2>');
  html = html.replace(/^# (.*$)/gim, '<h1>$1</h1>');

  // Bold & Italic
  html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
  html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');

  // Lists
  html = html.replace(/^\s*-\s+(.*$)/gim, '<li>$1</li>');
  html = html.replace(/((?:<li>.*?<\/li>\s*)+)/gs, '<ul>$1</ul>');

  // Ordered lists
  html = html.replace(/^\s*\d+\.\s+(.*$)/gim, '<li>$1</li>');

  // Horizontal rule
  html = html.replace(/^---$/gim, '<hr>');

  // Paragraphs
  const blocks = html.split(/\n\n+/);
  html = blocks.map(block => {
    block = block.trim();
    if (block.startsWith('<h') || block.startsWith('<pre') || block.startsWith('<ul') || block.startsWith('<ol') || block.startsWith('<table') || block.startsWith('<hr') || block.startsWith('<div')) {
      return block;
    }
    return `<p>${block}</p>`;
  }).join('\n\n');

  return html;
}

function renderTable(rows) {
  if (rows.length < 2) return '';
  const parseRow = r => r.split('|').slice(1, -1).map(c => c.trim());
  const header = parseRow(rows[0]);
  const bodyRows = rows.slice(2).map(parseRow);

  let html = '<div class="table-container"><table><thead><tr>';
  header.forEach(h => {
    html += `<th>${h}</th>`;
  });
  html += '</tr></thead><tbody>';
  bodyRows.forEach(row => {
    html += '<tr>';
    row.forEach(c => {
      html += `<td>${c}</td>`;
    });
    html += '</tr>';
  });
  html += '</tbody></table></div>';
  return html;
}

async function convertFile(mdPath, pdfPath, title) {
  const mdContent = fs.readFileSync(mdPath, 'utf8');
  const bodyHtml = mdToHtml(mdContent);

  const fullHtml = `
<!DOCTYPE html>
<html lang="ko">
<head>
  <meta charset="UTF-8">
  <title>${title}</title>
  <script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
  <style>
    @import url('https://cdn.jsdelivr.net/gh/orioncactus/pretendard/dist/web/static/pretendard.css');
    @page {
      size: A4;
      margin: 18mm 15mm 20mm 15mm;
    }
    body {
      font-family: Pretendard, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      color: #1e293b;
      line-height: 1.65;
      font-size: 12.5px;
      margin: 0;
      padding: 0;
      background: #ffffff;
    }
    header.doc-header {
      border-bottom: 3px solid #0f766e;
      padding-bottom: 10px;
      margin-bottom: 22px;
      display: flex;
      justify-content: space-between;
      align-items: flex-end;
    }
    header.doc-header .brand-title {
      font-size: 24px;
      color: #0f766e;
      margin: 0;
      font-weight: 800;
      letter-spacing: -0.03em;
      display: flex;
      align-items: center;
      gap: 8px;
    }
    header.doc-header .doc-subtitle {
      font-size: 12px;
      color: #64748b;
      font-weight: 600;
    }
    h1 {
      font-size: 20px;
      color: #0f766e;
      margin-top: 26px;
      margin-bottom: 12px;
      font-weight: 800;
      border-bottom: 1px solid #e2e8f0;
      padding-bottom: 6px;
      letter-spacing: -0.02em;
      page-break-after: avoid;
    }
    h2 {
      font-size: 16px;
      color: #0f766e;
      margin-top: 20px;
      margin-bottom: 10px;
      font-weight: 700;
      letter-spacing: -0.01em;
      page-break-after: avoid;
    }
    h3 {
      font-size: 13.5px;
      color: #334155;
      margin-top: 16px;
      margin-bottom: 8px;
      font-weight: 700;
      page-break-after: avoid;
    }
    p {
      margin: 8px 0;
      word-break: keep-all;
    }
    ul, ol {
      padding-left: 20px;
      margin: 8px 0;
    }
    li {
      margin-bottom: 4px;
      word-break: keep-all;
    }
    code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
      background: #f1f5f9;
      color: #0f766e;
      padding: 2px 6px;
      border-radius: 4px;
      font-size: 11px;
      border: 1px solid #e2e8f0;
    }
    pre {
      background: #0f172a;
      color: #f8fafc;
      padding: 12px 16px;
      border-radius: 8px;
      overflow-x: auto;
      font-size: 11px;
      line-height: 1.5;
      page-break-inside: avoid;
      box-shadow: inset 0 0 0 1px rgba(255,255,255,0.1);
    }
    pre code {
      background: transparent;
      color: inherit;
      padding: 0;
      border: none;
    }
    .table-container {
      margin: 16px 0;
      page-break-inside: avoid;
    }
    table {
      width: 100%;
      border-collapse: collapse;
      font-size: 11.5px;
      background: #ffffff;
    }
    th, td {
      border: 1px solid #cbd5e1;
      padding: 8px 12px;
      text-align: left;
    }
    th {
      background: #f0fdf4;
      color: #0f766e;
      font-weight: 700;
    }
    tr:nth-child(even) {
      background: #f8fafc;
    }
    hr {
      border: 0;
      height: 1px;
      background: #e2e8f0;
      margin: 20px 0;
    }
    strong {
      color: #0f172a;
    }
    .callout {
      padding: 12px 16px;
      border-radius: 8px;
      margin: 14px 0;
      page-break-inside: avoid;
      font-size: 12px;
    }
    .callout-note {
      background: #f0fdfa;
      border-left: 4px solid #0f766e;
      color: #115e59;
    }
    .callout-tip {
      background: #f0fdf4;
      border-left: 4px solid #16a34a;
      color: #166534;
    }
    .callout-warning {
      background: #fffbeb;
      border-left: 4px solid #f59e0b;
      color: #92400e;
    }
    .callout-title {
      font-weight: 700;
      margin-bottom: 4px;
    }
    .mermaid-container {
      display: flex;
      justify-content: center;
      margin: 18px 0;
      padding: 14px;
      background: #f8fafc;
      border: 1px solid #e2e8f0;
      border-radius: 10px;
      page-break-inside: avoid;
    }
    .mermaid {
      width: 100%;
      text-align: center;
    }
  </style>
</head>
<body>
  <header class="doc-header">
    <div class="brand-title">
      <span>kanpic</span>
    </div>
    <div class="doc-subtitle">${title} | Enterprise Official Document</div>
  </header>
  <div class="content">
    ${bodyHtml}
  </div>

  <script>
    mermaid.initialize({
      startOnLoad: true,
      theme: 'neutral',
      fontFamily: 'Pretendard, sans-serif'
    });
  </script>
</body>
</html>
  `;

  const browser = await chromium.launch();
  const page = await browser.newPage();
  await page.setContent(fullHtml, { waitUntil: 'networkidle' });

  // Wait for mermaid rendering to finish
  try {
    await page.waitForSelector('.mermaid svg', { timeout: 4000 });
  } catch (e) {
    // If no mermaid diagrams, ignore timeout
  }

  await page.pdf({
    path: pdfPath,
    format: 'A4',
    margin: { top: '16mm', bottom: '20mm', left: '15mm', right: '15mm' },
    printBackground: true,
    displayHeaderFooter: true,
    footerTemplate: `
      <div style="font-size: 9px; color: #94a3b8; width: 100%; padding: 0 15mm; display: flex; justify-content: space-between; font-family: Pretendard, sans-serif;">
        <span>kanpic Platform Official Documentation</span>
        <span>Page <span class="pageNumber"></span> of <span class="totalPages"></span></span>
      </div>
    `
  });
  await browser.close();
  console.log(`Generated high-quality PDF: ${pdfPath}`);
}

async function main() {
  const docsDir = path.join(__dirname, '../docs');
  await convertFile(path.join(docsDir, 'USER_GUIDE.md'), path.join(docsDir, 'USER_GUIDE.pdf'), '사용자 가이드 (User Guide)');
  await convertFile(path.join(docsDir, 'ADMIN_GUIDE.md'), path.join(docsDir, 'ADMIN_GUIDE.pdf'), '관리자 가이드 (Admin Guide)');
  await convertFile(path.join(docsDir, 'EXECUTIVE_REPORT.md'), path.join(docsDir, 'EXECUTIVE_REPORT.pdf'), '임원 보고서 (Executive Report)');
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});
