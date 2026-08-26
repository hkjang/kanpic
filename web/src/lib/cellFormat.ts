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
  // 음수 구역이 따로 있으면 그 구역이 부호를 스스로 적는다. (#,##0) 은
  // 괄호로 적겠다는 뜻이므로 빼기 부호를 덧붙이면 -(5) 가 된다.
  const sections=format.split(';')
  const negativeSection=value<0&&sections.length>1
  const section=(negativeSection?sections[1]:sections[0])??format
  const scientific=section.match(/^0(?:\.(0+))?[eE]([+-])(0+)$/)
  if(scientific){
    // 지수 자리는 서식에 적은 0 개수만큼 채운다. toExponential 은 자리를
    // 채우지 않아 "5.00E-1" 이 되었다. 엑셀·시트는 "5.00E-01" 이다.
    const mantissaDecimals=scientific[1]?.length??0,exponentDigits=scientific[3].length
    // toExponential 은 이진 실수를 반올림하므로 1.005가 1.00E+00이 된다.
    // 소수점만 옮겨 십진수 자리에서 반올림하면 1.01E+00이 되고, 서버가
    // 내는 답과 같아진다.
    let exponent=value===0?0:Math.floor(Math.log10(Math.abs(value)))
    const unit=10**mantissaDecimals
    let scaled=value===0?0:Math.round(Math.abs(shiftDecimalPoint(value,mantissaDecimals-exponent)))
    // log10 은 10의 거듭제곱에서 한 자리 어긋날 수 있다. 자릿수를 보고 맞춘다.
    if(scaled>=unit*10){scaled=Math.round(scaled/10);exponent+=1}
    else if(scaled>0&&scaled<unit){scaled=Math.round(scaled*10);exponent-=1}
    const digits=String(scaled).padStart(mantissaDecimals+1,'0')
    const point=digits.length-mantissaDecimals
    const mantissa=(value<0?'-':'')+(mantissaDecimals?digits.slice(0,point)+'.'+digits.slice(point):digits)
    const marker=exponent<0?'-':scientific[2]==='-'?'':'+'
    return `${mantissa}E${marker}${String(Math.abs(exponent)).padStart(exponentDigits,'0')}`
  }
  // 백분율은 100을 곱한 뒤 반올림한다. 이진 실수로 곱하면 1.005가
  // 100.49999…가 되어 100%로 내려앉는다. 사람이 적은 십진수 자리에서
  // 소수점만 옮기면 100.5가 되어 101%가 된다. 서버가 내는 답과 같다.
  const parsed=parseFormatSection(section)
  // 자리 기호가 하나도 없는 구역은 수를 적는 서식이 아니다. 날짜 서식에
  // 날짜가 될 수 없는 수가 들어오면 여기까지 흘러오는데, 그때 y 나 m 을
  // 글자로 적으면 "-yyyy-mm-dd1" 이 된다. 서버도 "-1" 을 적는다.
  const bare=!/[0#]/.test(parsed.core)
  const {core}=parsed,prefix=bare?'':parsed.prefix,suffix=bare?'':parsed.suffix
  const percent=core.includes('%')
  const base=negativeSection?-value:value
  const numeric=percent?shiftDecimalPoint(base,2):base
  const decimals=core.match(/\.(0+)/)?.[1].length??0
  const useGrouping=core.includes(',')
  const minimumIntegerDigits=Math.min(21,Math.max(1,(core.split('.')[0].match(/0/g)??[]).length))
  let rendered=new Intl.NumberFormat(locale,{useGrouping,minimumIntegerDigits,minimumFractionDigits:decimals,maximumFractionDigits:decimals}).format(Math.abs(numeric))
  // -0.2를 정수 자리로 반올림하면 0이 된다. 칸에 "-0"이 보이면 잘못 그린
  // 것처럼 읽히므로 부호를 붙이지 않는다. 서버도 그렇게 적는다.
  const roundedToZero=/^[0.,]*$/.test(rendered)
  if(percent)rendered+='%'
  rendered=prefix+rendered+suffix
  // 빼기 부호는 기호 앞에 온다. 엑셀은 -₩5 로 적지 ₩-5 로 적지 않는다.
  if(numeric<0&&!roundedToZero&&!/^-/.test(rendered))rendered='-'+rendered
  return rendered
}

/**
 * 서식 한 구역을 앞 글자·숫자 자리·뒤 글자로 나눈다.
 *
 * 엑셀에서 온 파일은 자리 기호만 적혀 있지 않다.
 *
 *   [$-409]#,##0     나라 코드다. 409 의 0 을 자릿수로 세면 5 가 05 가 된다.
 *   [$₩-412]#,##0    통화 기호는 대괄호 안의 $ 와 - 사이에 있다. 문자열에서
 *                    처음 만난 기호를 집으면 원화 파일이 달러로 보인다.
 *   #,##0"원"        따옴표 안은 그대로 적는 글자다.
 *   _-* #,##0_-      _ 는 자리 비우기, * 는 채우기다. 그리지 않는다.
 *
 * 자리 기호가 아닌 것을 세면 숫자가 달라 보이고, 사람은 그것을 값으로 읽는다.
 */
export function parseFormatSection(section:string){
  let prefix='',core='',suffix=''
  const put=(text:string)=>{if(core==='')prefix+=text;else suffix+=text}
  for(let index=0;index<section.length;index+=1){
    const character=section[index]
    if(character==='"'){
      const end=section.indexOf('"',index+1)
      put(section.slice(index+1,end<0?section.length:end))
      index=end<0?section.length:end
      continue
    }
    if(character==='['){
      const end=section.indexOf(']',index+1)
      const inside=section.slice(index+1,end<0?section.length:end)
      // [$기호-나라코드] 에서 기호만 꺼낸다. [Red] 나 [$-409] 는 그릴 것이 없다.
      if(inside.startsWith('$')){
        const symbol=inside.slice(1).split('-')[0]
        if(symbol!=='')put(symbol)
      }
      index=end<0?section.length:end
      continue
    }
    // _x 는 x 만큼 자리를 비우고, *x 는 x 로 채운다. 둘 다 값이 아니다.
    if(character==='_'||character==='*'){index+=1;continue}
    if(character==='\\'){put(section[index+1]??'');index+=1;continue}
    if('0#?,.%'.includes(character)){core+=character;continue}
    // 빈칸은 음수 구역의 괄호와 자리를 맞추려고 넣는 것이다. 그리면 "1,234 "
    // 처럼 꼬리가 붙는다. 서버도 그리지 않는다.
    if(character===' ')continue
    put(character)
  }
  return {prefix,core,suffix}
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

// 날 수를 날짜로 바꾸는 셈은 한 곳에만 둔다. 검증(lib/validation.ts)도
// 이것을 쓴다. 따로 세면 격자에 보이는 날짜와 검증이 읽는 날짜가 어긋난다.
export function spreadsheetDate(value:unknown){
  // 엑셀은 1900년을 윤년으로 잘못 센다. 일련번호 60은 없는 날(1900-02-29)을
  // 가리키므로, 그보다 작은 번호는 하루 뒤에서 세기 시작해야 1900-01-01이
  // 1번이 된다. 서버의 internal/formula 의 serialDate 가 같은 셈을 한다.
  // 음수는 날짜가 아니다. 표 프로그램에 1900년보다 앞선 날은 없다.
  // 서버의 serialDate 도 음수를 되돌린다.
  if(typeof value==='number'&&Number.isFinite(value)&&value>=0)
    return new Date((value<60?Date.UTC(1899,11,31):Date.UTC(1899,11,30))+Math.round(value*86400)*1000)
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

// shiftDecimalPoint 는 소수점만 옮긴다. 100을 곱하면 이진 실수의 어긋남이
// 따라오지만, 사람이 적은 십진수 표기에서 점을 옮기면 따라오지 않는다.
// 서버는 같은 일을 분수로 한다(internal/formula 의 decimalRound).
function shiftDecimalPoint(value:number,places:number){
  if(!Number.isFinite(value)||value===0)return value
  const text=value.toPrecision(15)
  if(text.includes('e')||text.includes('E'))return value*10**places
  const negative=text.startsWith('-'),body=negative?text.slice(1):text
  const point=body.indexOf('.'),digits=body.replace('.',''),whole=point<0?body.length:point
  const moved=whole+places
  let shifted:string
  if(moved<=0)shifted='0.'+'0'.repeat(-moved)+digits
  else if(moved>=digits.length)shifted=digits+'0'.repeat(moved-digits.length)
  else shifted=digits.slice(0,moved)+'.'+digits.slice(moved)
  return Number((negative?'-':'')+shifted)
}

function isDateFormat(format:string){
  const cleaned=format.replace(/"[^"]*"/g,'').replace(/\\./g,'').replace(/\[(?!h+\]|m+\]|s+\])[^\]]*]/gi,'')
  // mmm 과 mmmm 은 달 이름이므로 단위로 오해할 일이 없다. m 하나만으로는
  // 가르지 않는다 — "0.0 m" 처럼 단위로 적은 것을 달로 읽으면 안 된다.
  // 서버의 internal/formula 의 isDatePattern 이 같은 기준을 쓴다.
  return /[ydhs]/i.test(cleaned)||/mmm/i.test(cleaned)
}
