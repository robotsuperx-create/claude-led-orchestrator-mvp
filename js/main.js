/* js/main.js — تفاعلات الصفحة (JavaScript خالص بلا مكتبات) */
(function () {
  'use strict';

  // تشغيل الكود بعد جاهزية DOM
  function ready(fn) {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', fn);
    } else {
      fn();
    }
  }

  ready(function () {
    /* ---------- 1) قائمة التنقل ---------- */
    var navToggle = document.getElementById('nav-toggle');
    var navMenu = document.getElementById('nav-menu');

    if (navToggle && navMenu) {
      // تبديل فتح/إغلاق القائمة وتحديث aria-expanded
      navToggle.addEventListener('click', function () {
        var open = navMenu.classList.toggle('nav-open');
        navToggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      });

      // إغلاق القائمة عند النقر على أي رابط داخلها
      var navLinks = navMenu.querySelectorAll('a');
      for (var i = 0; i < navLinks.length; i++) {
        navLinks[i].addEventListener('click', function () {
          navMenu.classList.remove('nav-open');
          navToggle.setAttribute('aria-expanded', 'false');
        });
      }
    }

    /* ---------- 2) إظهار العناصر عند التمرير ---------- */
    var revealEls = document.querySelectorAll('.reveal');
    if (revealEls.length) {
      if ('IntersectionObserver' in window) {
        var observer = new IntersectionObserver(function (entries, obs) {
          entries.forEach(function (entry) {
            if (entry.isIntersecting) {
              entry.target.classList.add('visible');
              obs.unobserve(entry.target); // لا حاجة لمراقبته بعد الظهور
            }
          });
        }, { threshold: 0.1 });

        for (var j = 0; j < revealEls.length; j++) {
          observer.observe(revealEls[j]);
        }
      } else {
        // بديل عند عدم دعم IntersectionObserver: إظهار الكل مباشرة
        for (var k = 0; k < revealEls.length; k++) {
          revealEls[k].classList.add('visible');
        }
      }
    }

    /* ---------- 3) التحقق من نموذج التواصل ---------- */
    var form = document.getElementById('contact-form');
    if (form) {
      var nameField = document.getElementById('name');
      var emailField = document.getElementById('email');
      var messageField = document.getElementById('message');
      var statusEl = document.getElementById('form-status');

      // قيمة الحقل بعد إزالة المسافات (أو فارغة إن لم يوجد الحقل)
      var valueOf = function (el) {
        return el && typeof el.value === 'string' ? el.value.trim() : '';
      };

      // تبديل الصنف .error على الحقل حسب صلاحيته
      var mark = function (el, valid) {
        if (!el) return;
        if (valid) {
          el.classList.remove('error');
        } else {
          el.classList.add('error');
        }
      };

      var emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

      form.addEventListener('submit', function (event) {
        event.preventDefault(); // لا يُرسل أي شيء إلى خادم

        var nameOk = valueOf(nameField) !== '';
        var emailOk = emailPattern.test(valueOf(emailField));
        var messageOk = valueOf(messageField).length >= 10;

        mark(nameField, nameOk);
        mark(emailField, emailOk);
        mark(messageField, messageOk);

        if (statusEl) {
          statusEl.classList.remove('error', 'success');
        }

        if (!(nameOk && emailOk && messageOk)) {
          if (statusEl) {
            statusEl.textContent = 'يرجى تصحيح الحقول المحددة: الاسم مطلوب، والبريد الإلكتروني يجب أن يكون صحيحاً، والرسالة لا تقل عن 10 أحرف.';
            statusEl.classList.add('error');
          }
          return;
        }

        // نجاح: عرض رسالة وإفراغ النموذج
        if (statusEl) {
          statusEl.textContent = 'تم إرسال رسالتك بنجاح. شكراً لتواصلك معنا!';
          statusEl.classList.add('success');
        }
        form.reset();
      });
    }

    /* ---------- 4) السنة الحالية ---------- */
    var yearEl = document.getElementById('year');
    if (yearEl) {
      yearEl.textContent = String(new Date().getFullYear());
    }
  });
})();
