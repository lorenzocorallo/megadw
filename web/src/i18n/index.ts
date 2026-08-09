import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import common from "./locales/en/common.json";

void i18n.use(initReactI18next).init({
  lng: "en",
  fallbackLng: "en",
  interpolation: {
    escapeValue: false,
  },
  resources: {
    en: {
      common,
    },
  },
  defaultNS: "common",
  ns: ["common"],
});

export default i18n;
