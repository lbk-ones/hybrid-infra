import {Events} from "@wailsio/runtime";
import {GreetService} from "../bindings/hybrid-infra/go/services";
import {Alert} from "../bindings/hybrid-infra/go/services/uiservice.js";



window.onload = function (){
    var elementById = document.getElementById("alert");
    elementById.addEventListener("click",function (e){
        Alert("测试标题ggg","5秒后<span style=\"color:red\">6</span>自动关闭",0)
    })
}