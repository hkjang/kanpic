export type BorderStyle='thin'|'medium'|'thick'|'dashed'|'dotted'|'double'
export type BorderSide={style:BorderStyle;color:string}
export type CellBorders=Partial<Record<'top'|'right'|'bottom'|'left',BorderSide>>

export function formatCellValue(value:unknown,style?:Record<string,unknown>,locale='ko-KR'){
  if(value==null)return''
  // A chart value belongs on the canvas, never in a text cell or an input.
  if(typeof value==='object')return''
  const format=typeof style?.number_format==='string'?style.number_format.trim():''
  if(!format||format.toLowerCase()==='general')return String(value)
  if(format==='@')return String(value)
  if(isDateFormat(format)){
    const date=spreadsheetDate(value)
    if(date)return formatDate(date,format,locale)
  }
  if(typeof value!=='number'||!Number.isFinite(value))return String(value)
  const section=(value<0?format.split(';')[1]:format.split(';')[0])??format
  if(/[eE][+-]0+/.test(section)){
    const decimals=(section.match(/\.(0+)/)?.[1].length??2)
    return value.toExponential(decimals).replace('e','E')
  }
  const percent=section.includes('%'),parenthesized=value<0&&section.includes('(')&&section.includes(')'),numeric=(parenthesized?Math.abs(value):value)*(percent?100:1)
  const decimals=section.match(/\.(0+)/)?.[1].length??0
  const useGrouping=section.includes(',')
  const minimumIntegerDigits=Math.min(21,Math.max(1,(section.split('.')[0].match(/0/g)??[]).length))
  let rendered=new Intl.NumberFormat(locale,{useGrouping,minimumIntegerDigits,minimumFractionDigits:decimals,maximumFractionDigits:decimals}).format(numeric)
  if(percent)rendered+='%'
  const currency=section.match(/[₩$€¥]/)?.[0]
  if(currency)rendered=currency+rendered
  if(parenthesized)rendered=`(${rendered})`
  return rendered
}

export function wrapText(text:string,maxWidth:number,measure:(text:string)=>number){
  if(maxWidth<=0)return['']
  const lines:string[]=[]
  for(const paragraph of text.split('\n')){
    if(paragraph===''){lines.push('');continue}
    let line=''
    for(const token of paragraph.split(/(\s+)/).filter(Boolean)){
      const candidate=line+token
      if(line&&measure(candidate)>maxWidth){lines.push(line.trimEnd());line=token.trimStart()}else line=candidate
      while(line&&measure(line)>maxWidth){let length=1;while(length<line.length&&measure(line.slice(0,length+1))<=maxWidth)length+=1;lines.push(line.slice(0,length));line=line.slice(length)}
    }
    if(line||paragraph)lines.push(line.trimEnd())
  }
  return lines.length?lines:['']
}

function spreadsheetDate(value:unknown){
  if(typeof value==='number'&&Number.isFinite(value))return new Date(Date.UTC(1899,11,30)+value*86400000)
  if(typeof value==='string'){
    const parsed=new Date(value)
    if(Number.isFinite(parsed.getTime()))return parsed
  }
}

function formatDate(date:Date,format:string,locale:string){
  const normalized=format.replace(/\[[^hms\]]*]/gi,'').replace(/\[([hms]+)\]/gi,'$1').trim(),parts=normalized.split(/\s+/),dateIndex=parts.findIndex(part=>/[yd]/i.test(part)),timeIndex=parts.findIndex(part=>/[hs]/i.test(part))
  const pad=(value:number)=>String(value).padStart(2,'0'),monthName=new Intl.DateTimeFormat(locale,{timeZone:'UTC',month:'short'}).format(date)
  const renderDate=(pattern:string)=>pattern.replace(/yyyy|yy|mmmm|mmm|mm|m|dd|d/gi,token=>{switch(token.toLowerCase()){case'yyyy':return String(date.getUTCFullYear());case'yy':return pad(date.getUTCFullYear()%100);case'mmmm':case'mmm':return monthName;case'mm':return pad(date.getUTCMonth()+1);case'm':return String(date.getUTCMonth()+1);case'dd':return pad(date.getUTCDate());default:return String(date.getUTCDate())}})
  const renderTime=(pattern:string)=>{const twelve=/am\/pm/i.test(normalized),hour=twelve?(date.getUTCHours()%12||12):date.getUTCHours();return pattern.replace(/hh|h|mm|m|ss|s/gi,token=>{switch(token.toLowerCase()){case'hh':return pad(hour);case'h':return String(hour);case'mm':return pad(date.getUTCMinutes());case'm':return String(date.getUTCMinutes());case'ss':return pad(date.getUTCSeconds());default:return String(date.getUTCSeconds())}})}
  const rendered=parts.map((part,index)=>index===dateIndex?renderDate(part):index===timeIndex?renderTime(part):/^am\/pm$/i.test(part)?date.getUTCHours()<12?'AM':'PM':part)
  return rendered.join(' ')
}

function isDateFormat(format:string){
  const cleaned=format.replace(/"[^"]*"/g,'').replace(/\\./g,'').replace(/\[(?!h+\]|m+\]|s+\])[^\]]*]/gi,'')
  return /[ydhs]/i.test(cleaned)
}
