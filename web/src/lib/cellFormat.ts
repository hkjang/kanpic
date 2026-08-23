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
  // 엑셀은 1900년을 윤년으로 잘못 센다. 일련번호 60은 없는 날(1900-02-29)을
  // 가리키므로, 그보다 작은 번호는 하루 뒤에서 세기 시작해야 1900-01-01이
  // 1번이 된다. 서버의 internal/formula 의 serialDate 가 같은 셈을 한다.
  if(typeof value==='number'&&Number.isFinite(value))return new Date((value<60?Date.UTC(1899,11,31):Date.UTC(1899,11,30))+value*86400000)
  if(typeof value==='string'){
    const parsed=new Date(value)
    if(Number.isFinite(parsed.getTime()))return parsed
  }
}

// 서식 기호를 하나씩 읽어 그대로 적는다.
//
// 예전에는 서식을 공백으로 잘라 "날짜 부분 하나"와 "시각 부분 하나"만
// 그렸다. 그래서 "yyyy년 m월 d일" 처럼 토막이 셋인 서식은 앞 토막만
// 그려져 "2024년 m월 d일" 이 되었다. 한국어 날짜 서식이 그대로 깨졌다.
// "dddd" 와 "mmmm" 처럼 요일·달 이름만 적은 서식도 그리지 못했다.
//
// 표 서식에서 m 은 앞뒤를 봐야 뜻이 정해진다. 시 뒤에 오거나 초 앞에
// 오면 분이고, 그 밖에는 달이다. 서버의 internal/formula 의
// renderDatePattern 이 같은 규칙을 쓴다.
const DATE_TOKENS=['am/pm','a/p','yyyy','yy','mmmm','mmm','mm','m','dddd','ddd','dd','d','hh','h','ss','s']

function nextDateToken(format:string,index:number){
  const rest=format.slice(index).toLowerCase()
  for(const token of DATE_TOKENS)if(rest.startsWith(token))return token
  return ''
}

function secondFollows(format:string,index:number){
  while(index<format.length){
    const token=nextDateToken(format,index)
    if(!token){index+=1;continue}
    return token==='ss'||token==='s'
  }
  return false
}

function formatDate(date:Date,format:string,locale:string){
  const normalized=format.replace(/\[([hms]+)]/gi,'$1').replace(/\[[^\]]*]/g,'')
  const twelve=/am\/pm|a\/p/i.test(normalized)
  const pad=(value:number,two:boolean)=>two?String(value).padStart(2,'0'):String(value)
  const monthName=(long:boolean)=>new Intl.DateTimeFormat(locale,{timeZone:'UTC',month:long?'long':'short'}).format(date)
  const dayName=(long:boolean)=>new Intl.DateTimeFormat(locale,{timeZone:'UTC',weekday:long?'long':'short'}).format(date)
  let out='',index=0,previousWasHour=false
  while(index<normalized.length){
    const token=nextDateToken(normalized,index)
    if(!token){out+=normalized[index];index+=1;continue}
    switch(token){
      case'am/pm':out+=date.getUTCHours()<12?'AM':'PM';break
      case'a/p':out+=date.getUTCHours()<12?'A':'P';break
      case'yyyy':out+=String(date.getUTCFullYear());break
      case'yy':out+=pad(date.getUTCFullYear()%100,true);break
      case'mmmm':out+=monthName(true);break
      case'mmm':out+=monthName(false);break
      case'dddd':out+=dayName(true);break
      case'ddd':out+=dayName(false);break
      case'dd':case'd':out+=pad(date.getUTCDate(),token==='dd');break
      case'hh':case'h':{const hour=twelve?(date.getUTCHours()%12||12):date.getUTCHours();out+=pad(hour,token==='hh');break}
      case'ss':case's':out+=pad(date.getUTCSeconds(),token==='ss');break
      case'mm':case'm':
        // 시 뒤에 오거나 초 앞에 오면 분이다. 그 밖에는 달이다.
        out+=previousWasHour||secondFollows(normalized,index+token.length)
          ?pad(date.getUTCMinutes(),token==='mm')
          :pad(date.getUTCMonth()+1,token==='mm')
        break
    }
    previousWasHour=token==='hh'||token==='h'
    index+=token.length
  }
  return out
}

function isDateFormat(format:string){
  const cleaned=format.replace(/"[^"]*"/g,'').replace(/\\./g,'').replace(/\[(?!h+\]|m+\]|s+\])[^\]]*]/gi,'')
  // mmm 과 mmmm 은 달 이름이므로 단위로 오해할 일이 없다. m 하나만으로는
  // 가르지 않는다 — "0.0 m" 처럼 단위로 적은 것을 달로 읽으면 안 된다.
  // 서버의 internal/formula 의 isDatePattern 이 같은 기준을 쓴다.
  return /[ydhs]/i.test(cleaned)||/mmm/i.test(cleaned)
}
